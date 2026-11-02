package authtest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// IdP is a tiny OIDC provider (discovery, JWKS, authorize, token) with PKCE
// verification and switches to produce bad tokens.
type IdP struct {
	URL        string
	ClientID   string
	Expired    atomic.Bool // issue an already-expired ID token
	WrongNonce atomic.Bool // issue an ID token with a different nonce

	key   *rsa.PrivateKey
	mu    sync.Mutex
	codes map[string]grant
}

type grant struct{ nonce, challenge string }

func NewIdP(t testing.TB, clientID string) *IdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	i := &IdP{ClientID: clientID, key: key, codes: map[string]grant{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", i.discovery)
	mux.HandleFunc("GET /keys", i.jwks)
	mux.HandleFunc("GET /authorize", i.authorize)
	mux.HandleFunc("POST /token", i.token)
	srv := httptest.NewServer(mux)
	i.URL = srv.URL
	t.Cleanup(srv.Close)
	return i
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (i *IdP) discovery(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{
		"issuer":                                i.URL,
		"authorization_endpoint":                i.URL + "/authorize",
		"token_endpoint":                        i.URL + "/token",
		"jwks_uri":                              i.URL + "/keys",
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (i *IdP) jwks(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]any{"keys": []map[string]string{{
		"kty": "RSA", "use": "sig", "alg": "RS256", "kid": "k1",
		"n": b64(i.key.N.Bytes()),
		"e": b64(big.NewInt(int64(i.key.E)).Bytes()),
	}}})
}

// authorize auto-approves and redirects back with code+state.
func (i *IdP) authorize(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	code := rand.Text()
	i.mu.Lock()
	i.codes[code] = grant{nonce: q.Get("nonce"), challenge: q.Get("code_challenge")}
	i.mu.Unlock()

	u, err := url.Parse(q.Get("redirect_uri"))
	if err != nil {
		http.Error(w, "bad redirect_uri", http.StatusBadRequest)
		return
	}
	v := u.Query()
	v.Set("code", code)
	v.Set("state", q.Get("state"))
	u.RawQuery = v.Encode()
	http.Redirect(w, r, u.String(), http.StatusFound)
}

func (i *IdP) token(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	code := r.PostForm.Get("code")
	i.mu.Lock()
	g, ok := i.codes[code]
	delete(i.codes, code) // single use
	i.mu.Unlock()

	sum := sha256.Sum256([]byte(r.PostForm.Get("code_verifier")))
	if !ok || b64(sum[:]) != g.challenge {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
		return
	}
	nonce := g.nonce
	if i.WrongNonce.Load() {
		nonce = "not-the-nonce"
	}
	exp := time.Now().Add(time.Hour)
	if i.Expired.Load() {
		exp = time.Now().Add(-time.Hour)
	}
	idToken := i.sign(map[string]any{
		"iss": i.URL, "sub": "user-1", "aud": i.ClientID,
		"exp": exp.Unix(), "iat": time.Now().Add(-2 * time.Hour).Unix(),
		"nonce": nonce, "email": "u@example.com", "name": "Test User",
	})
	writeJSON(w, map[string]any{
		"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": idToken,
	})
}

func (i *IdP) sign(claims map[string]any) string {
	payload, _ := json.Marshal(claims)
	in := b64([]byte(`{"alg":"RS256","typ":"JWT","kid":"k1"}`)) + "." + b64(payload)
	h := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, i.key, crypto.SHA256, h[:])
	if err != nil {
		panic(err)
	}
	return in + "." + b64(sig)
}

// Authorize plays the browser: follows loginLocation to the IdP and returns
// the code and state it redirects back with.
func (i *IdP) Authorize(t testing.TB, loginLocation string) (code, state string) {
	t.Helper()
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := c.Get(loginLocation)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	loc, err := resp.Location()
	if err != nil {
		t.Fatalf("authorize did not redirect: %v", err)
	}
	return loc.Query().Get("code"), loc.Query().Get("state")
}