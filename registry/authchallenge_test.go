package registry

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"strings"
	"testing"
)

type TraceTransport struct {
}

func (TraceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req != nil {
		if b, dumpErr := httputil.DumpRequestOut(req, true); dumpErr == nil {
			fmt.Println(string(b))
		} else {
			fmt.Printf("*** ERROR DUMPING REQUEST: %v\n", dumpErr)
		}
	}

	resp, err := http.DefaultTransport.RoundTrip(req)

	if resp != nil {
		if b, dumpErr := httputil.DumpResponse(resp, true); dumpErr == nil {
			fmt.Println(string(b))
		} else {
			fmt.Printf("*** ERROR DUMPING RESPONSE: %v\n", dumpErr)
		}
	}

	return resp, err
}

func Test_AuthenticationDance(t *testing.T) {
	tcs := []struct {
		name    string
		handler func(t *testing.T) func(w http.ResponseWriter, r *http.Request)
		checker func(t *testing.T, err error)
	}{
		{
			name: "No authentication",
			handler: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {}
			},
			checker: func(t *testing.T, err error) {
				if err != nil {
					t.Errorf("Expected no error, got %v", err)
				}
			},
		},
		{
			name: "OAuth",
			handler: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v2/" {
						auth := r.Header.Get("Authorization")
						if auth == "" {
							w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="http://%v/oauth2/token",service="%v"`, r.Host, r.Host))
							w.WriteHeader(http.StatusUnauthorized)
							return
						}
						if auth != "Bearer token" {
							t.Errorf("invalid token in ping request")
							w.WriteHeader(http.StatusInternalServerError)
							return
						}

						return
					}

					if r.URL.Path == "/oauth2/token" {
						auth := r.Header.Get("Authorization")
						if auth == "" {
							// Let's be polite and send the WWW-Authenticate header, even though some registries don't (they're explicitly tested elsewhere)
							w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
							w.WriteHeader(http.StatusUnauthorized)
							return
						}
						if auth != "Basic dXNlcm5hbWU6cGFzc3dvcmQ=" {
							t.Errorf("invalid credentials in oauth authentication request")
							w.WriteHeader(http.StatusInternalServerError)
							return
						}
						w.Write([]byte(`{"token":"token"}`))
						return
					}
					t.Errorf("unexpected path = %v", r.URL.Path)
				}
			},
			checker: func(t *testing.T, err error) {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Azure",
			handler: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v2/" {
						auth := r.Header.Get("Authorization")
						if auth == "" {
							w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="http://%v/oauth2/token",service="%v"`, r.Host, r.Host))
							w.WriteHeader(http.StatusUnauthorized)
							return
						}
						if auth != "Bearer token" {
							t.Errorf("invalid token in ping request")
							w.WriteHeader(http.StatusInternalServerError)
							return
						}

						return
					}

					if r.URL.Path == "/oauth2/token" {
						auth := r.Header.Get("Authorization")
						if auth == "" {
							// Azure doesn't send the WWW-Authenticate header
							w.WriteHeader(http.StatusUnauthorized)
							return
						}
						if auth != "Basic dXNlcm5hbWU6cGFzc3dvcmQ=" {
							t.Errorf("invalid credentials in oauth authentication request")
							w.WriteHeader(http.StatusInternalServerError)
							return
						}
						w.Write([]byte(`{"access_token":"token"}`))
						return
					}
					t.Errorf("unexpected path = %v", r.URL.Path)
				}
			},
			checker: func(t *testing.T, err error) {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			},
		},
		{
			name: "Token extract error",
			handler: func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
				return func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/v2/" {
						auth := r.Header.Get("Authorization")
						if auth == "" {
							w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Bearer realm="http://%v/oauth2/token",service="%v"`, r.Host, r.Host))
							w.WriteHeader(http.StatusUnauthorized)
							return
						}
						if auth != "Bearer token" {
							t.Errorf("invalid token in ping request")
							w.WriteHeader(http.StatusInternalServerError)
							return
						}

						return
					}

					if r.URL.Path == "/oauth2/token" {
						auth := r.Header.Get("Authorization")
						if auth == "" {
							// Let's be polite and send the WWW-Authenticate header, even though some registries don't (they're explicitly tested elsewhere)
							w.Header().Set("WWW-Authenticate", `Basic realm="registry"`)
							w.WriteHeader(http.StatusUnauthorized)
							return
						}
						if auth != "Basic dXNlcm5hbWU6cGFzc3dvcmQ=" {
							t.Errorf("invalid credentials in oauth authentication request")
							w.WriteHeader(http.StatusInternalServerError)
							return
						}
						w.Write([]byte(`{"unrecognized_token":"token"}`))
						return
					}
					t.Errorf("unexpected path = %v", r.URL.Path)
				}
			},
			checker: func(t *testing.T, err error) {
				if err == nil {
					t.Fatal("Expected an error but did not get one")
				}
				if !strings.HasSuffix(err.Error(), "unable to extract token") {
					t.Fatalf("Expected token parsing error, got: %v", err)
				}
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(tc.handler(t)))
			defer server.Close()

			r, err := NewWithTransport(server.URL, "username", "password", TraceTransport{})
			if err != nil {
				t.Fatalf("unexpected error creating registry with transport: %v", err)
			}

			err = r.Ping()
			tc.checker(t, err)
		})
	}
}
