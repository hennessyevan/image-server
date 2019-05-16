package http

import (
	"net/http"
)

type CorsHandler struct {
	http.Handler
}

func (h *CorsHandler) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	enableCors(rw)
	if (*req).Method == "OPTIONS" {
		return
	}
	h.Handler.ServeHTTP(rw, req)
}

func enableCors(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS, PUT, DELETE")
	w.Header().Set("Access-Control-Allow-Headers", "Accept, Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization")
}
