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
	"regexp"
	"strings"
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
			name:     "dtr with 0 repositories in the _catalog response",
			handler:  dtrDataSource(0),
			expected: []string{"project2/repo1", "project2/repo2", "project3/repo1", "project3/repo2", "project4/repo1", "project4/repo2"},
		},
		{
			name:     "dtr with 1 page in the _catalog response",
			handler:  dtrDataSource(1),
			expected: []string{"project2/repo1", "project2/repo2", "project3/repo1", "project3/repo2", "project4/repo1", "project4/repo2"},
		},
		{
			name:     "dtr with multiple pages in the _catalog response",
			handler:  dtrDataSource(2),
			expected: []string{"project2/repo1", "project2/repo2", "project3/repo1", "project3/repo2", "project4/repo1", "project4/repo2"},
		},
		{
			name:     "harbor",
			handler:  harborDataSource,
			expected: []string{"project2/repo1", "project2/repo2", "project3/repo1", "project3/repo2", "project4/repo1", "project4/repo2"},
		},
		{
			name:     "harbor v2.0",
			handler:  harborV2DataSource,
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

// We've learned that sometimes DTR returns no repositories in the response to
// /v2/_catalog, and sometimes it returns some repositories (but not all). Either
// way, we want to use /api/v0/repositories instead.
func dtrDataSource(catalogPages int) func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
	return func(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
		return func(w http.ResponseWriter, r *http.Request) {
			log.Printf("dtrDataSource test handler got request for %v", r.URL.String())
			if r.Header.Get("Authorization") == "" {
				w.Header().Set("Www-Authenticate", fmt.Sprintf(`Bearer realm="https://%v/auth/token",service="dtr",scope="registry:catalog:*"`, r.Host))
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			if r.URL.Path == "/auth/token" {
				w.Header().Set("Content-Type", "application/json")
				w.Write([]byte(`{"token":"x","access_token":"y"}`))
				return
			}

			if r.URL.Path == "/v2/_catalog" {
				switch catalogPages {
				case 0:
					w.Header().Add("Content-Type", "application/json; charset=utf-8")
					_, _ = w.Write([]byte(`{"repositories": [""]}`))
				case 1:
				default:
					w.Header().Set("Link", `</v2/_catalog?last=x&n=100>; rel="next"`)
				}
				w.Header().Add("Content-Type", "application/json; charset=utf-8")
				_, _ = w.Write([]byte(`{"repositories": ["x"]}`))
				return
			}

			if r.URL.Path == "/api/v0/repositories/" {
				w.Header().Add("Content-Type", "application/json; charset=utf-8")

				switch r.URL.Query().Get("pageStart") {
				case "":
					w.Header().Add("X-Next-Page-Start", "0000-repo2")
					_, _ = w.Write([]byte(`{"repositories":[{"namespace":"project2","name":"repo1"}]}`))
				case "0000-repo2":
					_, _ = w.Write([]byte(`{"repositories":[{"namespace":"project2","name":"repo2"},{"namespace":"project3","name":"repo1"},{"namespace":"project3","name":"repo2"},{"namespace":"project4","name":"repo1"},{"namespace":"project4","name":"repo2"}]}`))
				}

				return
			}

			w.WriteHeader(http.StatusPaymentRequired)
		}
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
				w.WriteHeader(http.StatusForbidden)
				t.Fatal("should use basic pre-auth for Harbor")
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

func harborV2DataSource(t *testing.T) func(w http.ResponseWriter, r *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		listRepositoriesURL := regexp.MustCompile(`/api/v2\.0/projects/project[1-4]/repositories`)

		log.Printf("harborV2 test handler got request for %v", r.URL.String())

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

		// Harbor V1 request URL
		if r.URL.Path == "/api/projects" {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		if r.URL.Path == "/api/v2.0/projects" {
			if h, ok := r.Header["Authorization"]; !ok || len(h) < 1 || h[0] != "Basic dXNlcjpwYXNz" {
				w.WriteHeader(http.StatusForbidden)
				t.Fatal("should use basic pre-auth for Harbor")
				return
			}

			w.Header()["Link"] = []string{fmt.Sprint(`</api/v2.0/projects`)}

			w.WriteHeader(http.StatusOK)

			w.Write([]byte(`[
				{"project_id":1, "repo_count":0, "name":"project1"},
				{"project_id":2, "repo_count":2, "name":"project2"},
				{"project_id":3, "repo_count":2, "name":"project3"},
				{"project_id":4, "repo_count":2, "name":"project4"}]`))

			return
		}

		/* check if any of the API requests match
		* /api/v2.0/projects/project1/repositories
		* /api/v2.0/projects/project2/repositories
		* ... etc
		 */
		if listRepositoriesURL.MatchString(r.URL.Path) {
			if h, ok := r.Header["Authorization"]; !ok || len(h) < 1 || h[0] != "Basic dXNlcjpwYXNz" {
				w.Header().Set("WWW-Authenticate", "Basic")
				w.WriteHeader(http.StatusUnauthorized)
				return
			}

			// determine which project was sent by removing the characters surrounding the project name
			// resulting string would be equal to project1, project2, etc.
			projectName := strings.Replace(r.URL.Path, "/api/v2.0/projects/", "", 1)
			projectName = strings.Replace(projectName, "/repositories", "", 1)

			switch projectName {
			case "project1":
				t.Errorf("project 1 has 0 repos and was called for the repo list but should not have been")
				w.WriteHeader(http.StatusNotFound)
				return
			case "project2", "project3", "project4":
				w.WriteHeader(http.StatusOK)
				w.Write([]byte(fmt.Sprintf(`[{"name":"%v/repo1"},{"name":"%v/repo2"}]`, projectName, projectName)))
			default:
				t.Errorf("code asked for project %v but we never mentioned that", projectName)
				w.WriteHeader(http.StatusNotFound)
			}

			return
		}

		t.Error(r.URL.Path)
		w.WriteHeader(http.StatusPaymentRequired)
	}
}
