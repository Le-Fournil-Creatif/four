package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Project struct {
	Name   string `json:"name"`
	Dir    string `json:"dir"`
	Legacy bool   `json:"legacy"` // ancien emplacement (FOUR_LEGACY)
	Git    bool   `json:"git"`
	Loaded bool   `json:"loaded"`

	Remote     string `json:"remote"`
	Owner      string `json:"owner"`
	Repo       string `json:"repo"`
	Branch     string `json:"branch"`
	Upstream   bool   `json:"upstream"`
	Ahead      int    `json:"ahead"`
	Behind     int    `json:"behind"`
	Dirty      int    `json:"dirty"`     // fichiers suivis modifiés
	Untracked  int    `json:"untracked"` // nouveaux fichiers
	LastCommit int64  `json:"last"`
	HasCommits bool   `json:"commits"`
}

func (p *Project) GitHub() bool { return p.Owner != "" && p.Repo != "" }

func (p *Project) URL() string {
	if !p.GitHub() {
		return ""
	}
	return "github.com/" + p.Owner + "/" + p.Repo
}

func gitCmd(dir string, args ...string) *exec.Cmd {
	a := append([]string{"-c", "safe.directory=*", "-c", "core.quotePath=false", "-C", dir}, args...)
	c := exec.Command("git", a...)
	c.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	return c
}

func gitOut(dir string, args ...string) (string, error) {
	out, err := gitCmd(dir, args...).Output()
	return strings.TrimRight(string(out), "\n"), err
}

// Liste rapide (lecture des dossiers, sans git) : l'écran s'affiche tout de
// suite, l'état git arrive juste après.
func scanProjects() []*Project {
	var ps []*Project
	seen := map[string]bool{}
	scan := func(base string, legacy bool) {
		ents, err := os.ReadDir(base)
		if err != nil {
			return
		}
		for _, e := range ents {
			name := e.Name()
			if strings.HasPrefix(name, ".") || seen[name] {
				continue
			}
			dir := filepath.Join(base, name)
			fi, err := os.Stat(dir)
			if err != nil || !fi.IsDir() {
				continue
			}
			isGit := exists(filepath.Join(dir, ".git"))
			if legacy && !isGit {
				continue // dans l'ancien emplacement, seuls les dépôts git sont des projets
			}
			seen[name] = true
			ps = append(ps, &Project{Name: name, Dir: dir, Legacy: legacy, Git: isGit})
		}
	}
	scan(Root, false)
	if Legacy != "" && Legacy != Root {
		scan(Legacy, true)
	}
	return ps
}

func parseRemote(url string) (owner, repo string) {
	u := strings.TrimSpace(url)
	for _, p := range []string{"https://github.com/", "http://github.com/", "ssh://git@github.com/", "git@github.com:", "github.com/"} {
		if strings.HasPrefix(u, p) {
			u = strings.TrimPrefix(u, p)
			u = strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
			if i := strings.IndexByte(u, '/'); i > 0 && i < len(u)-1 && !strings.Contains(u[i+1:], "/") {
				return u[:i], u[i+1:]
			}
			return "", ""
		}
	}
	// https://user@github.com/o/r
	if i := strings.Index(u, "@github.com/"); i > 0 && strings.HasPrefix(u, "https://") {
		return parseRemote("https://github.com/" + u[i+len("@github.com/"):])
	}
	return "", ""
}

func loadGit(p *Project) {
	p.Loaded = true
	if !p.Git {
		p.Git = exists(filepath.Join(p.Dir, ".git"))
		if !p.Git {
			return
		}
	}
	out, err := gitOut(p.Dir, "status", "--porcelain=v2", "--branch")
	if err == nil {
		p.Dirty, p.Untracked, p.Upstream, p.Ahead, p.Behind = 0, 0, false, 0, 0
		p.HasCommits = true
		for _, l := range strings.Split(out, "\n") {
			switch {
			case l == "":
			case strings.HasPrefix(l, "# branch.oid "):
				p.HasCommits = !strings.HasSuffix(l, "(initial)")
			case strings.HasPrefix(l, "# branch.head "):
				p.Branch = strings.TrimPrefix(l, "# branch.head ")
				if p.Branch == "(detached)" {
					p.Branch = "détaché"
				}
			case strings.HasPrefix(l, "# branch.upstream "):
				p.Upstream = true
			case strings.HasPrefix(l, "# branch.ab "):
				f := strings.Fields(strings.TrimPrefix(l, "# branch.ab "))
				if len(f) == 2 {
					p.Ahead, _ = strconv.Atoi(strings.TrimPrefix(f[0], "+"))
					p.Behind, _ = strconv.Atoi(strings.TrimPrefix(f[1], "-"))
				}
			case l[0] == '#':
			case l[0] == '?':
				p.Untracked++
			default:
				p.Dirty++
			}
		}
	}
	p.Remote, _ = gitOut(p.Dir, "config", "--get", "remote.origin.url")
	p.Owner, p.Repo = parseRemote(p.Remote)
	if t, err := gitOut(p.Dir, "log", "-1", "--format=%ct"); err == nil {
		p.LastCommit, _ = strconv.ParseInt(strings.TrimSpace(t), 10, 64)
	}
}

func loadAll(ps []*Project) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, p := range ps {
		wg.Add(1)
		sem <- struct{}{}
		go func(p *Project) {
			defer func() { <-sem; wg.Done() }()
			loadGit(p)
		}(p)
	}
	wg.Wait()
}

// Ordre : /srv/repos puis l'ancien emplacement ; dans chaque groupe, les
// projets liés à GitHub d'abord, puis par dernier commit.
func sortProjects(ps []*Project) {
	sort.SliceStable(ps, func(i, j int) bool {
		a, b := ps[i], ps[j]
		if a.Legacy != b.Legacy {
			return !a.Legacy
		}
		if a.GitHub() != b.GitHub() {
			return a.GitHub()
		}
		if a.LastCommit != b.LastCommit {
			return a.LastCommit > b.LastCommit
		}
		return a.Name < b.Name
	})
}

// Cache : le menu s'ouvre avec l'état de la dernière fois, puis se met à jour.
func cachePath() string { return filepath.Join(stateDir(), "projects.json") }

func loadProjectCache() []*Project {
	b, err := os.ReadFile(cachePath())
	if err != nil {
		return nil
	}
	var ps []*Project
	if json.Unmarshal(b, &ps) != nil {
		return nil
	}
	// Ne garder que ce qui existe encore, et ajouter les nouveaux dossiers.
	fresh := scanProjects()
	byDir := map[string]*Project{}
	for _, p := range ps {
		byDir[p.Dir] = p
	}
	out := make([]*Project, 0, len(fresh))
	for _, f := range fresh {
		if c, ok := byDir[f.Dir]; ok {
			c.Legacy = f.Legacy
			out = append(out, c)
		} else {
			out = append(out, f)
		}
	}
	return out
}

func saveProjectCache(ps []*Project) {
	b, err := json.Marshal(ps)
	if err != nil {
		return
	}
	tmp := cachePath() + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, cachePath())
	}
}

// ── Détail du projet sélectionné ────────────────────────────────────────

type Commit struct {
	Hash, Author, Email, Subject string
	When                         int64
}

type Change struct{ Code, Path string }

type Details struct {
	Commits []Commit
	Changes []Change
}

func loadDetails(dir string) Details {
	var d Details
	if out, err := gitOut(dir, "log", "-12", "--format=%h%x1f%ct%x1f%an%x1f%ae%x1f%s"); err == nil && out != "" {
		for _, l := range strings.Split(out, "\n") {
			f := strings.Split(l, "\x1f")
			if len(f) < 5 {
				continue
			}
			t, _ := strconv.ParseInt(f[1], 10, 64)
			d.Commits = append(d.Commits, Commit{Hash: f[0], When: t, Author: f[2], Email: f[3], Subject: f[4]})
		}
	}
	if out, err := gitOut(dir, "status", "--porcelain"); err == nil && out != "" {
		for _, l := range strings.Split(out, "\n") {
			if len(l) < 4 {
				continue
			}
			code := strings.TrimSpace(l[:2])
			if code == "??" {
				code = "?"
			}
			d.Changes = append(d.Changes, Change{Code: code, Path: l[3:]})
		}
	}
	return d
}

// Auteur d'un commit, ramené au compte de l'équipe quand on le reconnaît.
func shortAuthor(name, email string) string {
	low := strings.ToLower(name + " " + email)
	for _, m := range lookupGroup(GroupName).members {
		if strings.Contains(low, strings.ToLower(m)) {
			return m
		}
	}
	f := strings.Fields(name)
	if len(f) == 0 {
		return "?"
	}
	s := strings.ToLower(f[0])
	if r := []rune(s); len(r) > 8 {
		s = string(r[:8])
	}
	return s
}

// git pull --ff-only, seulement si l'arbre est propre et suivi : jamais de
// fusion surprise sous les pieds de Claude.
func pullIfClean(p *Project) string {
	if !p.Git {
		return ""
	}
	loadGit(p)
	if !p.Upstream {
		return ""
	}
	if p.Dirty > 0 {
		return "git pull ignoré : fichiers modifiés"
	}
	before, _ := gitOut(p.Dir, "rev-parse", "--short", "HEAD")
	c := gitCmd(p.Dir, "pull", "--ff-only", "--quiet")
	done := make(chan error, 1)
	if err := c.Start(); err != nil {
		return "git pull impossible"
	}
	go func() { done <- c.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return "git pull a échoué, on continue sans"
		}
	case <-time.After(30 * time.Second):
		_ = c.Process.Kill()
		return "git pull trop long, abandonné"
	}
	after, _ := gitOut(p.Dir, "rev-parse", "--short", "HEAD")
	loadGit(p)
	if before == after {
		return "à jour"
	}
	return "git pull " + before + " → " + after
}
