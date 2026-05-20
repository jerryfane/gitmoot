package git

import (
	"fmt"
	"strings"
)

type Repo struct {
	Owner string
	Name  string
}

func (r Repo) String() string {
	if r.Owner == "" || r.Name == "" {
		return ""
	}
	return r.Owner + "/" + r.Name
}

func ParseGitHubRemote(remote string) (Repo, error) {
	path := strings.TrimSpace(remote)
	switch {
	case strings.HasPrefix(path, "https://github.com/"):
		path = strings.TrimPrefix(path, "https://github.com/")
	case strings.HasPrefix(path, "git@github.com:"):
		path = strings.TrimPrefix(path, "git@github.com:")
	default:
		return Repo{}, fmt.Errorf("unsupported GitHub remote: %s", remote)
	}

	path = strings.TrimSuffix(strings.Trim(path, "/"), ".git")
	parts := strings.Split(path, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return Repo{}, fmt.Errorf("unsupported GitHub remote: %s", remote)
	}
	return Repo{Owner: parts[0], Name: parts[1]}, nil
}
