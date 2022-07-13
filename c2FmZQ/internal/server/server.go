//
// Copyright 2021-2022 TTBT Enterprises LLC
//
// This file is part of c2FmZQ (https://c2FmZQ.org/).
//
// c2FmZQ is free software: you can redistribute it and/or modify it under the
// terms of the GNU General Public License as published by the Free Software
// Foundation, either version 3 of the License, or (at your option) any later
// version.
//
// c2FmZQ is distributed in the hope that it will be useful, but WITHOUT ANY
// WARRANTY; without even the implied warranty of MERCHANTABILITY or FITNESS FOR
// A PARTICULAR PURPOSE. See the GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License along with
// c2FmZQ. If not, see <https://www.gnu.org/licenses/>.

// Package server implements the Stingle server API.
package server

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/NYTimes/gziphandler"
	"github.com/hashicorp/golang-lru"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/crypto/acme/autocert"
	"golang.org/x/time/rate"

	"c2FmZQ/internal/database"
	"c2FmZQ/internal/jsclient"
	"c2FmZQ/internal/log"
	"c2FmZQ/internal/server/basicauth"
	"c2FmZQ/internal/server/limit"
	"c2FmZQ/internal/stingle"
	"c2FmZQ/internal/stingle/token"
)

type ctxKey int

var (
	connKey ctxKey = 1

	reqLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "server_response_time",
			Help:    "The server's response time",
			Buckets: []float64{0.01, 0.05, 0.1, 0.2, 0.3, 0.4, 0.5, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 20, 30, 45, 60, 90, 120},
		},
		[]string{"method", "uri"},
	)
	reqStatus = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "server_response_status_total",
			Help: "Number of requests",
		},
		[]string{"method", "uri", "status"},
	)
	reqSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "server_request_size",
			Help:    "The size of requests",
			Buckets: prometheus.ExponentialBuckets(1, 2, 32),
		},
		[]string{"code"},
	)
	respSize = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "server_response_size",
			Help:    "The size of responses",
			Buckets: prometheus.ExponentialBuckets(1, 2, 32),
		},
		[]string{"code"},
	)

	startTime time.Time
)

func init() {
	startTime = time.Now()

	prometheus.MustRegister(reqLatency)
	prometheus.MustRegister(reqStatus)
	prometheus.MustRegister(reqSize)
	prometheus.MustRegister(respSize)
}

// An HTTP server that implements the Stingle server API.
type Server struct {
	AllowCreateAccount     bool
	AutoApproveNewAccounts bool
	BaseURL                string
	Redirect404            string
	MaxConcurrentRequests  int
	EnableWebApp           bool
	mux                    *http.ServeMux
	srv                    *http.Server
	db                     *database.Database
	addr                   string
	basicAuth              *basicauth.BasicAuth
	pathPrefix             string
	preLoginCache          *lru.Cache
	checkKeyCache          *lru.Cache
}

// New returns an instance of Server that's fully initialized and ready to run.
func New(db *database.Database, addr, htdigest, pathPrefix string) *Server {
	s := &Server{
		MaxConcurrentRequests: 5,
		mux:                   http.NewServeMux(),
		db:                    db,
		addr:                  addr,
		pathPrefix:            pathPrefix,
	}
	cache, err := lru.New(10000)
	if err != nil {
		log.Fatalf("lru.New: %v", err)
	}
	s.preLoginCache = cache
	if cache, err = lru.New(10000); err != nil {
		log.Fatalf("lru.New: %v", err)
	}
	s.checkKeyCache = cache
	if htdigest != "" {
		var err error
		if s.basicAuth, err = basicauth.New(htdigest); err != nil {
			log.Errorf("htdigest: %v", err)
		}
	}
	if s.basicAuth != nil {
		s.mux.HandleFunc(pathPrefix+"/metrics", s.basicAuth.Handler("Metrics", promhttp.Handler()))
	}

	if pathPrefix != "" {
		s.mux.HandleFunc("/", s.handleNotFound)
	}
	s.mux.HandleFunc(pathPrefix+"/", func(w http.ResponseWriter, req *http.Request) {
		if !s.EnableWebApp {
			http.NotFound(w, req)
			return
		}
		log.Infof("%s %s %s", req.Proto, req.Method, req.RequestURI)

		p := strings.TrimPrefix(req.URL.Path, pathPrefix+"/")
		if p == "" {
			p = "index.html"
		}
		b, err := jsclient.FS.ReadFile(p)
		if err != nil {
			http.NotFound(w, req)
			return
		}
		http.ServeContent(w, req, p, startTime, bytes.NewReader(b))
	})

	s.mux.HandleFunc(pathPrefix+"/v2/", s.noauth(s.handleNotImplemented))
	s.mux.HandleFunc(pathPrefix+"/v2/register/createAccount", s.noauth(s.handleCreateAccount))
	s.mux.HandleFunc(pathPrefix+"/v2/login/preLogin", s.noauth(s.handlePreLogin))
	s.mux.HandleFunc(pathPrefix+"/v2/login/login", s.noauth(s.handleLogin))
	s.mux.HandleFunc(pathPrefix+"/v2/login/logout", s.auth(s.handleLogout))
	s.mux.HandleFunc(pathPrefix+"/v2/login/changePass", s.auth(s.handleChangePass))
	s.mux.HandleFunc(pathPrefix+"/v2/login/checkKey", s.noauth(s.handleCheckKey))
	s.mux.HandleFunc(pathPrefix+"/v2/login/recoverAccount", s.noauth(s.handleRecoverAccount))
	s.mux.HandleFunc(pathPrefix+"/v2/login/deleteUser", s.auth(s.handleDeleteUser))
	s.mux.HandleFunc(pathPrefix+"/v2/login/changeEmail", s.auth(s.handleChangeEmail))
	s.mux.HandleFunc(pathPrefix+"/v2/keys/getServerPK", s.auth(s.handleGetServerPK))
	s.mux.HandleFunc(pathPrefix+"/v2/keys/reuploadKeys", s.auth(s.handleReuploadKeys))

	s.mux.HandleFunc(pathPrefix+"/v2/sync/getUpdates", s.auth(s.handleGetUpdates))
	s.mux.HandleFunc(pathPrefix+"/v2/sync/upload", s.method("POST", s.handleUpload))
	s.mux.HandleFunc(pathPrefix+"/v2/sync/moveFile", s.auth(s.handleMoveFile))
	s.mux.HandleFunc(pathPrefix+"/v2/sync/emptyTrash", s.auth(s.handleEmptyTrash))
	s.mux.HandleFunc(pathPrefix+"/v2/sync/delete", s.auth(s.handleDelete))
	s.mux.HandleFunc(pathPrefix+"/v2/sync/download", s.method("POST", s.handleDownload))
	s.mux.HandleFunc(pathPrefix+"/v2/download/", s.method("GET", s.handleTokenDownload))
	s.mux.HandleFunc(pathPrefix+"/v2/sync/getDownloadUrls", s.auth(s.handleGetDownloadUrls))
	s.mux.HandleFunc(pathPrefix+"/v2/sync/getUrl", s.auth(s.handleGetURL))

	s.mux.HandleFunc(pathPrefix+"/v2/sync/addAlbum", s.auth(s.handleAddAlbum))
	s.mux.HandleFunc(pathPrefix+"/v2/sync/deleteAlbum", s.auth(s.handleDeleteAlbum))
	s.mux.HandleFunc(pathPrefix+"/v2/sync/changeAlbumCover", s.auth(s.handleChangeAlbumCover))
	s.mux.HandleFunc(pathPrefix+"/v2/sync/renameAlbum", s.auth(s.handleRenameAlbum))
	s.mux.HandleFunc(pathPrefix+"/v2/sync/getContact", s.auth(s.handleGetContact))
	s.mux.HandleFunc(pathPrefix+"/v2/sync/share", s.auth(s.handleShare))
	s.mux.HandleFunc(pathPrefix+"/v2/sync/editPerms", s.auth(s.handleEditPerms))
	s.mux.HandleFunc(pathPrefix+"/v2/sync/removeAlbumMember", s.auth(s.handleRemoveAlbumMember))
	s.mux.HandleFunc(pathPrefix+"/v2/sync/unshareAlbum", s.auth(s.handleUnshareAlbum))
	s.mux.HandleFunc(pathPrefix+"/v2/sync/leaveAlbum", s.auth(s.handleLeaveAlbum))

	s.mux.HandleFunc(pathPrefix+"/c2/config/echo", s.method("POST", s.handleEcho))
	s.mux.HandleFunc(pathPrefix+"/c2/config/generateOTP", s.auth(s.handleGenerateOTP))
	s.mux.HandleFunc(pathPrefix+"/c2/config/setOTP", s.auth(s.handleSetOTP))
	s.mux.HandleFunc(pathPrefix+"/c2/admin/users", s.auth(s.handleAdminUsers))

	return s
}

func (s *Server) wrapHandler() http.Handler {
	handler := http.Handler(s.mux)
	handler = gziphandler.GzipHandler(handler)
	handler = limit.New(s.MaxConcurrentRequests, handler)
	handler = promhttp.InstrumentHandlerRequestSize(reqSize, handler)
	handler = promhttp.InstrumentHandlerResponseSize(respSize, handler)
	return handler
}

func (s *Server) httpServer() *http.Server {
	s.srv = &http.Server{
		Addr:              s.addr,
		Handler:           s.wrapHandler(),
		ReadHeaderTimeout: 30 * time.Second,
		IdleTimeout:       10 * time.Second,
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, connKey, c)
		},
		ErrorLog: log.Logger(),
		TLSConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
		},
	}
	return s.srv
}

// Run runs the HTTP server on the configured address.
func (s *Server) Run() error {
	return s.httpServer().ListenAndServe()
}

// RunWithTLS runs the HTTP server with TLS.
func (s *Server) RunWithTLS(certFile, keyFile string) error {
	return s.httpServer().ListenAndServeTLS(certFile, keyFile)
}

// RunWithAutocert runs the HTTP server with TLS credentials provided by
// letsencrypt.org.
func (s *Server) RunWithAutocert(domain, addr string) error {
	certManager := autocert.Manager{
		Prompt: autocert.AcceptTOS,
		Cache:  s.db.AutocertCache(),
	}
	if domain != "any" && domain != "*" {
		certManager.HostPolicy = autocert.HostWhitelist(strings.Split(domain, ",")...)
	}
	go func() {
		if addr == "" {
			addr = ":http"
		}
		log.Fatalf("autocert.Manager failed: %v", http.ListenAndServe(addr, certManager.HTTPHandler(nil)))
	}()

	s.srv = s.httpServer()
	s.srv.TLSConfig.GetCertificate = certManager.GetCertificate
	return s.srv.ListenAndServeTLS("", "")
}

// RunWithListener runs the server using a pre-existing Listener. Used for testing.
func (s *Server) RunWithListener(l net.Listener) error {
	s.srv = &http.Server{
		Addr:    s.addr,
		Handler: s.wrapHandler(),
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			return context.WithValue(ctx, connKey, c)
		},
	}
	return s.srv.Serve(l)
}

// Shutdown cleanly shuts down the http server.
func (s *Server) Shutdown() error {
	return s.srv.Shutdown(context.Background())
}

// Handler returns the server's http.Handler. Used for testing.
func (s *Server) Handler() http.Handler {
	return s.wrapHandler()
}

// decodeParams decodes the params value that's parsed to most API endpoints.
// It is an encrypted json object representing key:value pairs.
// Returns the decrypted key:value pairs as a map.
func (s *Server) decodeParams(params string, user database.User) (map[string]string, error) {
	sk, err := s.db.DecryptSecretKey(user.ServerSecretKey)
	if err != nil {
		return nil, err
	}
	defer sk.Wipe()
	m, err := stingle.DecryptMessage(params, user.PublicKey, sk)
	if err != nil {
		return nil, err
	}
	var p map[string]string
	if err := json.Unmarshal(m, &p); err != nil {
		return nil, err
	}
	log.Debugf("Params: %#v", p)
	return p, nil
}

// parseInt converts a string to int64, mapping any errors to a default return
// value.
func parseInt(s string, def int64) int64 {
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return def
	}
	return v
}

func (s *Server) setDeadline(ctx context.Context, t time.Time) {
	c, ok := ctx.Value(connKey).(net.Conn)
	if !ok {
		log.Debugf("ctx doesn't have connKey")
		return
	}
	c.SetDeadline(t)
}

// method wraps handlers to enforce a specific method.
func (s *Server) method(method string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if req.Method != method {
			reqStatus.WithLabelValues(req.Method, req.URL.String(), "nok").Inc()
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		next(w, req)
	}
}

// noauth wraps handlers that don't require authentication.
func (s *Server) noauth(f func(*http.Request) *stingle.Response) http.HandlerFunc {
	rl := rate.NewLimiter(rate.Limit(0.5), 1)
	return s.method("POST", func(w http.ResponseWriter, req *http.Request) {
		timer := prometheus.NewTimer(reqLatency.WithLabelValues(req.Method, req.URL.String()))
		defer timer.ObserveDuration()
		s.setDeadline(req.Context(), time.Now().Add(30*time.Second))
		defer s.setDeadline(req.Context(), time.Time{})
		log.Infof("%s %s %s", req.Proto, req.Method, req.URL)
		req.ParseForm()
		if err := rl.Wait(req.Context()); err != nil {
			return
		}
		sr := f(req)
		if err := sr.Send(w); err != nil {
			log.Errorf("Send: %v", err)
		}
		reqStatus.WithLabelValues(req.Method, req.URL.String(), sr.Status).Inc()
	})
}

// checkToken validates the signed token that was given to the client when it
// logged in. The client presents this token with most API requests.
// Returns the decoded token, and the authenticated user.
func (s *Server) checkToken(tok, scope string) (token.Token, database.User, error) {
	id, err := token.Subject(tok)
	if err != nil {
		return token.Token{}, database.User{}, err
	}
	user, err := s.db.UserByID(id)
	if err != nil {
		return token.Token{}, database.User{}, err
	}
	tk, err := s.db.DecryptTokenKey(user.TokenKey)
	if err != nil {
		return token.Token{}, database.User{}, err
	}
	defer tk.Wipe()
	t, err := token.Decrypt(tk, tok)
	if err != nil {
		return token.Token{}, database.User{}, err
	}
	if t.Scope != scope {
		return token.Token{}, database.User{}, token.ErrValidationFailed
	}
	return t, user, nil
}

// auth wraps handlers that require authentication, checking the token, and
// passing the authenticated user to the underlying handler.
func (s *Server) auth(f func(database.User, *http.Request) *stingle.Response) http.HandlerFunc {
	return s.method("POST", func(w http.ResponseWriter, req *http.Request) {
		timer := prometheus.NewTimer(reqLatency.WithLabelValues(req.Method, req.URL.String()))
		defer timer.ObserveDuration()
		s.setDeadline(req.Context(), time.Now().Add(30*time.Second))
		defer s.setDeadline(req.Context(), time.Time{})

		req.ParseForm()

		tok := req.PostFormValue("token")
		_, user, err := s.checkToken(tok, "session")
		if err != nil || !user.ValidTokens[token.Hash(tok)] {
			log.Errorf("%s %s (INVALID TOKEN: %v)", req.Method, req.URL, err)
			sr := stingle.ResponseNOK().AddPart("logout", "1").AddError("You are not logged in")
			if err := sr.Send(w); err != nil {
				log.Errorf("Send: %v", err)
			}
			return
		}
		log.Infof("%s %s %s (UserID:%d)", req.Proto, req.Method, req.URL, user.UserID)
		sr := f(user, req)
		if err := sr.Send(w); err != nil {
			log.Errorf("Send: %v", err)
		}
		reqStatus.WithLabelValues(req.Method, req.URL.String(), sr.Status).Inc()
	})
}

// handleNotFound handles requests for undefined endpoints.
func (s *Server) handleNotFound(w http.ResponseWriter, req *http.Request) {
	if log.Level >= log.DebugLevel {
		log.Debugf("!!! (404) %s %s", req.Method, req.URL)
		req.ParseForm()
		if req.PostForm != nil {
			for k, v := range req.PostForm {
				log.Debugf("> %s: %v", k, v)
			}
		}
	}
	if s.Redirect404 != "" {
		http.Redirect(w, req, s.Redirect404, http.StatusFound)
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

// handleNotImplemented returns an error to the user saying this functionality
// is not implemented.
func (s *Server) handleNotImplemented(req *http.Request) *stingle.Response {
	return stingle.ResponseNOK().AddError("This functionality is not yet implemented in the server")
}

// handleEcho handles the /c2/config/echo endpoint. It is used to test client
// features. In particular, it is used the javascript client to test whether
// streaming upload is supported by the browser.
//
// Arguments:
//  - w: The http response writer.
//  - req: The http request.
//
// Form arguments
//  - token: The signed session token.
//  - echo: An arbitrary string to be returned to the client.
//
// Returns:
//  - stingle.Response("ok")
//        Parts("echo", the arbitrary string)
func (s *Server) handleEcho(w http.ResponseWriter, req *http.Request) {
	log.Infof("%s %s %s", req.Proto, req.Method, req.RequestURI)
	buf := make([]byte, 4096)
	n, err := io.ReadFull(req.Body, buf)
	if err != io.EOF && err != io.ErrUnexpectedEOF {
		log.Errorf("handleEcho: ReadFull failed: %v", err)
		http.Error(w, "Internal Error", http.StatusInternalServerError)
		return
	}
	body := string(buf[:n])
	form, err := url.ParseQuery(body)
	if err != nil {
		log.Errorf("handleEcho: ParseQuery failed: %v", err)
		http.Error(w, body, http.StatusInternalServerError)
		return
	}
	if !form.Has("token") {
		http.Error(w, body, http.StatusInternalServerError)
		return
	}
	tok := form.Get("token")
	if _, user, err := s.checkToken(tok, "session"); err != nil || !user.ValidTokens[token.Hash(tok)] {
		http.Error(w, body, http.StatusInternalServerError)
		return
	}
	stingle.ResponseOK().AddPart("echo", form.Get("echo")).Send(w)
}
