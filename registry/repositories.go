package registry

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

type repositoriesResponse struct {
	Repositories []string `json:"repositories"`
}

func (registry *Registry) Repositories() ([]string, error) {
	repos := make([]string, 0, 10)

	rchan, echan := registry.StreamRepositories(context.Background())

	for {
		select {
		case r, ok := <-rchan:
			if !ok {
				return repos, nil
			}
			repos = append(repos, r)
		case e := <-echan:
			return repos, e
		}
	}
}

func (registry *Registry) StreamRepositories(ctx context.Context) (<-chan string, <-chan error) {
	regChan := make(chan string)
	errChan := make(chan error)

	registry.resetToken()

	go func() {
		// defer close(errChan)
		defer close(regChan)

		regurl := registry.url("/v2/_catalog")

		var err error //We create this here, otherwise url will be rescoped with :=
		var response repositoriesResponse

		gotSome := false
		for {
			select {
			case <-ctx.Done():
				return
			default:
				registry.Logf("registry.repositories url=%s", regurl)
				regurl, err = registry.getPaginatedJson(regurl, &response)
				switch err {
				case ErrNoMorePages:
					// If we have not gotten anything yet and we get 0 repositories back, it could be because
					// there are legitimately 0 repositories or it could be because the registry is DTR and it
					// wants us to use the DTR API instead.
					//
					// If the fallback fails, we'll assume that there are legitimately 0 repositories and return
					// without an error.
					if !gotSome && len(response.Repositories) == 0 {
						_ = registry.tryFallback(ctx, regChan, errChan)
						return
					}
					streamRegistryAPIRepositoriesPage(ctx, regChan, response.Repositories)
					return

				case nil:
					gotSome = true
					if !streamRegistryAPIRepositoriesPage(ctx, regChan, response.Repositories) {
						return
					}
					continue

				default:
					if ue, ok := err.(*url.Error); ok {
						if he, ok := ue.Err.(*HttpStatusError); ok {
							if he.Response.StatusCode == http.StatusUnauthorized {
								if fallbackErr := registry.tryFallback(ctx, regChan, errChan); fallbackErr == nil {
									return
								}
							}
						}
					}

					errChan <- err
					return
				}
			}
		}
	}()

	return regChan, errChan
}

func (registry *Registry) tryFallback(ctx context.Context, regChan chan string, errChan chan error) error {
	regurl := registry.url("/api/v0/repositories/")
	registry.Logf("attempting DTR fallback at %v", regurl)

	gotSome := false
	for {
		var err2 error

		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			dtrRepositories := struct {
				Repositories []dtrRepository `json:"repositories"`
			}{}

			regurl, err2 = registry.getPaginatedJson(regurl, &dtrRepositories)

			switch err2 {
			case ErrNoMorePages:
				gotSome = true
				streamDTRAPIRepositoriesPage(ctx, regChan, dtrRepositories.Repositories)
				return nil
			case nil:
				gotSome = true
				if !streamDTRAPIRepositoriesPage(ctx, regChan, dtrRepositories.Repositories) {
					return nil
				}
				continue
			default:
				if gotSome {
					// we got something successfully but now we're failing, return the current error
					errChan <- err2
					return nil
				}

				// try Harbor fallback
				regurl = registry.url("/api/projects")
				registry.Logf("got error %v, attempting Harbor fallback at %v", err2, regurl)
				gotSome := false
				for {
					var err3 error
					select {
					case <-ctx.Done():
						return ctx.Err()
					default:
						harborProjects := []harborProject{}

						regurl, err3 = registry.getPaginatedJson(regurl, &harborProjects)

						switch err3 {
						case ErrNoMorePages:
							gotSome = true
							streamHarborProjectsPage(ctx, registry, regChan, errChan, harborProjects)
							return nil
						case nil:
							gotSome = true
							if !streamHarborProjectsPage(ctx, registry, regChan, errChan, harborProjects) {
								return nil
							}
							continue
						default:
							if gotSome {
								// we got something successfully but now we're failing, return the current error
								errChan <- err3
								return nil
							}

							// the fallbacks didn't work, return the original error
							return fmt.Errorf("fallback didn't work")
						}
					}
				}
			}
		}
	}
}

type dtrRepository struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Status    string `json:"status"`
}

type harborProject struct {
	ID        int `json:"project_id"`
	RepoCount int `json:"repo_count"`
	// there are more fields but we don't care about them
}

type harborRepo struct {
	Name string `json:"name"`
	// there are more fields but we don't care about them
}

func streamRegistryAPIRepositoriesPage(ctx context.Context, c chan string, v []string) bool {
	for _, r := range v {
		select {
		case <-ctx.Done():
			return false
		case c <- r:
			// next
		}
	}
	return true
}

func streamDTRAPIRepositoriesPage(ctx context.Context, c chan string, v []dtrRepository) bool {
	for _, r := range v {
		select {
		case <-ctx.Done():
			return false
		case c <- fmt.Sprintf("%s/%s", r.Namespace, r.Name):
			// next
		}
	}
	return true
}

func streamHarborProjectsPage(ctx context.Context, registry *Registry, c chan string, e chan error, v []harborProject) bool {
	for _, project := range v {
		if project.RepoCount <= 0 {
			continue
		}

		if !streamHarborProjectRepos(ctx, project, registry, c, e) {
			return false
		}
	}

	return true
}

func streamHarborProjectRepos(ctx context.Context, project harborProject, registry *Registry, c chan string, e chan error) bool {
	u := registry.url(fmt.Sprintf("/api/repositories?project_id=%d", project.ID))

	for {
		var err error

		select {
		case <-ctx.Done():
			return false
		default:
			harborRepos := []harborRepo{}

			u, err = registry.getPaginatedJson(u, &harborRepos)

			switch err {
			case ErrNoMorePages:
				streamHarborReposPage(ctx, c, harborRepos)
				return true
			case nil:
				if !streamHarborReposPage(ctx, c, harborRepos) {
					return false
				}
				continue
			default:
				// we got something successfully but now we're failing, return the current error
				e <- err
				return false
			}
		}
	}
}

func streamHarborReposPage(ctx context.Context, c chan string, v []harborRepo) bool {
	for _, r := range v {
		select {
		case <-ctx.Done():
			return false
		case c <- r.Name:
			// next
		}
	}
	return true
}
