package main

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type GHRepo struct {
	Owner    string    `json:"owner"`
	Name     string    `json:"name"`
	URL      string    `json:"url"`
	Desc     string    `json:"desc"`
	Private  bool      `json:"private"`
	Archived bool      `json:"archived"`
	Pushed   time.Time `json:"pushed"`
}

func (r GHRepo) Full() string { return r.Owner + "/" + r.Name }

func ghInstalled() bool { _, err := exec.LookPath("gh"); return err == nil }

func ghCmd(args ...string) *exec.Cmd {
	c := exec.Command("gh", args...)
	c.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "GH_NO_UPDATE_NOTIFIER=1", "NO_COLOR=1")
	return c
}

func ghOut(args ...string) (string, error) {
	c := ghCmd(args...)
	var stderr strings.Builder
	c.Stderr = &stderr
	out, err := c.Output()
	if err != nil {
		if m := firstLine(stderr.String()); m != "" {
			return "", errors.New(m)
		}
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// Connexion GitHub présente ? (lecture du fichier, sans réseau)
func ghLoggedInFast() bool {
	dir := os.Getenv("GH_CONFIG_DIR")
	if dir == "" {
		dir = filepath.Join(home, ".config", "gh")
	}
	b, err := os.ReadFile(filepath.Join(dir, "hosts.yml"))
	return err == nil && strings.Contains(string(b), "github.com")
}

type GHUser struct {
	Login, Name string
	ID          string
}

func ghUser() (GHUser, error) {
	out, err := ghOut("api", "user", "--jq", `[.login, (.name // ""), (.id|tostring)] | @tsv`)
	if err != nil {
		return GHUser{}, err
	}
	f := strings.Split(out, "\t")
	if len(f) < 3 {
		return GHUser{}, errors.New("réponse GitHub inattendue")
	}
	return GHUser{Login: f[0], Name: f[1], ID: f[2]}, nil
}

func ghOrgs() []string {
	out, err := ghOut("api", "user/orgs", "--jq", ".[].login")
	if err != nil || out == "" {
		return nil
	}
	return strings.Split(out, "\n")
}

// Comptes où l'on peut créer un repo : l'orga par défaut d'abord, puis le
// compte perso, puis les autres orgas.
func ghOwners() []string {
	u, err := ghUser()
	orgs := ghOrgs()
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, o := range orgs {
		if o == DefaultOwner {
			add(o)
		}
	}
	if err == nil {
		add(u.Login)
	}
	for _, o := range orgs {
		add(o)
	}
	if len(out) == 0 {
		add(DefaultOwner)
	}
	return out
}

func ghCacheFile() string { return filepath.Join(stateDir(), "gh-repos.json") }

func cachedGHRepos() []GHRepo {
	var rs []GHRepo
	if b, err := os.ReadFile(ghCacheFile()); err == nil {
		_ = json.Unmarshal(b, &rs)
	}
	return rs
}

// Tous les repos accessibles : les miens, ceux des orgas, ceux où je suis
// collaborateur.
func fetchGHRepos() ([]GHRepo, error) {
	out, err := ghOut("api", "--paginate",
		"user/repos?per_page=100&sort=pushed&affiliation=owner,collaborator,organization_member",
		"--jq", `.[] | [.owner.login, .name, (.private|tostring), (.pushed_at // ""), .html_url, (.archived|tostring), ((.description // "") | gsub("[\t\n\r]"; " "))] | @tsv`)
	if err != nil {
		return nil, err
	}
	var rs []GHRepo
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(l, "\t")
		if len(f) < 7 {
			continue
		}
		t, _ := time.Parse(time.RFC3339, f[3])
		rs = append(rs, GHRepo{Owner: f[0], Name: f[1], Private: f[2] == "true", Pushed: t,
			URL: f[4], Archived: f[5] == "true", Desc: f[6]})
	}
	sort.SliceStable(rs, func(i, j int) bool { return rs[i].Pushed.After(rs[j].Pushed) })
	if b, err := json.Marshal(rs); err == nil {
		_ = os.WriteFile(ghCacheFile(), b, 0o600)
	}
	return rs, nil
}

// owner/nom depuis une URL collée, « owner/nom » ou git@github.com:owner/nom.
func parseRepoInput(s string) (owner, name string) {
	s = strings.TrimSpace(s)
	if o, r := parseRemote(s); o != "" {
		return o, r
	}
	s = strings.TrimPrefix(strings.TrimPrefix(s, "https://"), "http://")
	if o, r := parseRemote(s); o != "" {
		return o, r
	}
	p := strings.Split(strings.TrimSuffix(s, ".git"), "/")
	if len(p) == 2 && validName(p[0]) && validName(p[1]) {
		return p[0], p[1]
	}
	return "", ""
}

func ghClone(owner, name, dest string) error {
	c := ghCmd("repo", "clone", owner+"/"+name, dest, "--", "-c", "core.sharedRepository=group", "--quiet")
	out, err := c.CombinedOutput()
	if err != nil {
		return errors.New(gitError(string(out)))
	}
	return nil
}

func ghCreate(owner, name string, private bool, dir string) error {
	if owner == "" {
		return errors.New("compte GitHub pas encore chargé : réessaie dans un instant")
	}
	vis := "--private"
	if !private {
		vis = "--public"
	}
	c := ghCmd("repo", "create", owner+"/"+name, vis, "--source", dir, "--remote", "origin")
	out, err := c.CombinedOutput()
	if err != nil {
		return errors.New(lastLine(string(out)))
	}
	return nil
}

// Message utile d'une sortie gh/git : la ligne « fatal: » plutôt que
// « failed to run git: exit status 128 ».
func gitError(out string) string {
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "fatal: ") || strings.HasPrefix(l, "error: ") || strings.HasPrefix(l, "ERROR: ") {
			l = l[strings.IndexByte(l, ' ')+1:]
			switch {
			case strings.Contains(l, "Permission denied"):
				return "écriture refusée : " + l
			case strings.Contains(l, "already exists and is not an empty"):
				return "le dossier existe déjà et n'est pas vide"
			case strings.Contains(l, "not found") || strings.Contains(l, "Could not resolve"):
				return "repo introuvable, ou pas d'accès avec ce compte GitHub"
			}
			return l
		}
	}
	return lastLine(out)
}
