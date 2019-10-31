package registry_test

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"

	"github.com/heroku/docker-registry-client/registry"
)

type regErrors struct {
	Errors []regError `json:"errors"`
}

type regError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func Test_Registry_Repositories(t *testing.T) {
	tcs := []struct {
		name     string
		handler  func(t *testing.T) func(w http.ResponseWriter, r *http.Request)
		expected []string
	}{
		{
			name:     "harbor",
			handler:  harborDataSource,
			expected: []string{"project2/repo1", "project2/repo2", "project3/repo1", "project3/repo2", "project4/repo1", "project4/repo2"},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewTLSServer(http.HandlerFunc(tc.handler(t)))
			defer ts.Close()

			u, _ := url.Parse(ts.URL)

			reg, _ := registry.NewWithTransport(fmt.Sprintf("https://%s", u.Host), "user", "pass", &http.Transport{
				TLSClientConfig: &tls.Config{
					InsecureSkipVerify: true,
				},
			})

			repos, err := reg.Repositories()

			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(repos, tc.expected) {
				t.Errorf("Got %v but expected %v", repos, tc.expected)
			}
		})
	}
}

func harborDataSource(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("test handler got request for %v", r.URL.String())

		if r.URL.Path == "/v2/_catalog" {
			w.WriteHeader(http.StatusUnauthorized)

			buf, _ := json.Marshal(&regErrors{
				Errors: []regError{
					{
						Code:    "UNAUTHORIZED",
						Message: "authentication required",
					},
				},
			})
			w.Write(buf)

			return
		}

		if r.URL.Path == "/api/v0/repositories/" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		if r.URL.Path == "/api/projects" {
			if h, ok := r.Header["Authorization"]; !ok || len(h) < 1 || h[0] != "Basic dXNlcjpwYXNz" {
				w.Header().Set("WWW-Authenticate", "Basic")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			var nextPage string
			page := r.URL.Query().Get("page")
			pageSize := r.URL.Query().Get("page_size")

			if pageSize == "" {
				pageSize = "100"
			}

			switch page {
			case "":
				nextPage = "2"
			case "2":
				nextPage = ""
			default:
				t.Errorf("Invalid page number %v", page)
				w.WriteHeader(http.StatusNotFound)
				return
			}

			if nextPage != "" {
				w.Header()["Link"] = []string{fmt.Sprintf(`</api/projects?page=%v&page_size=%v>; rel="next"`, nextPage, pageSize)}
			}

			w.WriteHeader(http.StatusOK)

			switch page {
			case "":
				w.Write([]byte(`[{"project_id":1, "repo_count":0},{"project_id":2, "repo_count":2},{"project_id":3, "repo_count":2}]`))
			case "2":
				w.Write([]byte(`[{"project_id":4, "repo_count":2}]`))
			}

			return
		}

		if r.URL.Path == "/api/repositories" {
			if h, ok := r.Header["Authorization"]; !ok || len(h) < 1 || h[0] != "Basic dXNlcjpwYXNz" {
				w.Header().Set("WWW-Authenticate", "Basic")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			var nextPage string

			projectID := r.URL.Query().Get("project_id")

			page := r.URL.Query().Get("page")
			pageSize := r.URL.Query().Get("page_size")

			if pageSize == "" {
				pageSize = "100"
			}

			switch page {
			case "", "1":
				page = "1"
				nextPage = "2"
			case "2":
				nextPage = ""
			default:
				t.Errorf("Invalid page number %v", page)
				w.WriteHeader(http.StatusNotFound)
				return
			}

			if nextPage != "" {
				w.Header()["Link"] = []string{fmt.Sprintf(`</api/repositories?project_id=%v&page=%v&page_size=%v>; rel="next"`, projectID, nextPage, pageSize)}
			}

			switch projectID {
			case "1":
				t.Errorf("project 1 has 0 repos and was called for the repo list but should not have been")
				w.WriteHeader(http.StatusNotFound)
				return
			case "2", "3", "4":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(fmt.Sprintf(`[{"name":"project%v/repo%v"}]`, projectID, page)))
			default:
				t.Errorf("code asked for project %v but we never mentioned that", projectID)
				w.WriteHeader(http.StatusNotFound)
			}

			return
		}

		t.Error(r.URL.Path)
		w.WriteHeader(http.StatusPaymentRequired)
	}
}
