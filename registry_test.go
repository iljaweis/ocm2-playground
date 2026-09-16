package playground_test

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-containerregistry/pkg/registry"
)

func NewRegistry(t *testing.T, username, password *string, withTLS bool) (ref string, ca []byte) {
	t.Helper()
	handler := registry.New(registry.WithReferrersSupport(true))
	if username != nil && password != nil {
		handler = basicAuthHandler(handler, *username, *password)
	}

	var server *httptest.Server
	if withTLS {
		server = httptest.NewTLSServer(handler)
		ca = pem.EncodeToMemory(&pem.Block{
			Type:  "CERTIFICATE",
			Bytes: server.Certificate().Raw,
		})

		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(ca) {
			t.Fatalf("failed to append CA certificate to pool")
		}

		original := http.DefaultTransport
		trusted := original.(*http.Transport).Clone()
		trusted.TLSClientConfig = &tls.Config{RootCAs: pool}
		http.DefaultTransport = trusted

		t.Cleanup(func() { http.DefaultTransport = original })
	} else {
		server = httptest.NewServer(handler)
	}

	t.Cleanup(func() { server.Close() })

	u, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("failed to parse registry URL: %v", err)
	}
	ref = u.Host

	return
}

func basicAuthHandler(h http.Handler, username, password string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if u, p, ok := r.BasicAuth(); ok && username == u && password == p {
			h.ServeHTTP(w, r)
			return
		}
		w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	})
}
