package main

import (
	"bufio"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var (
	Root         = envOr("FOUR_ROOT", "/srv/repos")
	Legacy       = os.Getenv("FOUR_LEGACY") // ancien emplacement, facultatif
	DefaultOwner = os.Getenv("FOUR_OWNER")  // compte GitHub proposé en premier
	Socket       = envOr("FOUR_SOCKET", "four")
	GroupName    = "devs"
	Bin          = selfPath()
)

// Comptes du groupe à ne pas montrer comme coéquipiers (FOUR_HIDE=a,b).
var hiddenUsers = func() map[string]bool {
	set := map[string]bool{}
	for _, u := range strings.Split(os.Getenv("FOUR_HIDE"), ",") {
		if u = strings.TrimSpace(u); u != "" {
			set[u] = true
		}
	}
	return set
}()

var me, meName, home = currentUser()

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func currentUser() (login, display, dir string) {
	u, err := user.Current()
	if err != nil {
		h, _ := os.UserHomeDir()
		return os.Getenv("USER"), os.Getenv("USER"), h
	}
	display = strings.TrimSpace(strings.Split(u.Name, ",")[0])
	if display == "" {
		r := []rune(u.Username)
		if len(r) > 0 {
			r[0] = unicode.ToUpper(r[0])
		}
		display = string(r)
	}
	return u.Username, display, u.HomeDir
}

func selfPath() string {
	if p, err := os.Executable(); err == nil {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	return "four"
}

func stateDir() string {
	d := filepath.Join(home, ".cache", "four")
	_ = os.MkdirAll(d, 0o755)
	return d
}

func markDir() string {
	d := filepath.Join(stateDir(), "mark")
	_ = os.MkdirAll(d, 0o755)
	return d
}

func shareDir() string    { return filepath.Join(Root, ".four") }
func presenceDir() string { return filepath.Join(shareDir(), "presence") }

// Projets qui touchent la prod : badge ▲ prod, et jamais de mode sans
// confirmation. Liste partagée, un nom par ligne, dans <racine>/.four/prod.
func prodSet() map[string]bool { return listFile("prod") }

// Claude démarre-t-il sans demander de confirmation ? Non par défaut : il faut
// citer le projet (ou *) dans <racine>/.four/auto, et prod l'emporte toujours.
func autoMode(project string) bool {
	if prodSet()[project] {
		return false
	}
	auto := listFile("auto")
	return auto["*"] || auto[project]
}

// Une liste partagée de <racine>/.four : une entrée par ligne, # pour commenter.
func listFile(name string) map[string]bool {
	set := map[string]bool{}
	f, err := os.Open(filepath.Join(shareDir(), name))
	if err != nil {
		return set
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l != "" && !strings.HasPrefix(l, "#") {
			set[l] = true
		}
	}
	return set
}

// ── Groupe devs ─────────────────────────────────────────────────────────

type groupInfo struct {
	gid     int
	members []string
	ok      bool
}

func lookupGroup(name string) groupInfo {
	f, err := os.Open("/etc/group")
	if err != nil {
		return groupInfo{}
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		p := strings.Split(sc.Text(), ":")
		if len(p) < 4 || p[0] != name {
			continue
		}
		gid, _ := strconv.Atoi(p[2])
		var m []string
		for _, x := range strings.Split(p[3], ",") {
			if x = strings.TrimSpace(x); x != "" {
				m = append(m, x)
			}
		}
		return groupInfo{gid: gid, members: m, ok: true}
	}
	return groupInfo{}
}

func (g groupInfo) has(u string) bool {
	for _, m := range g.members {
		if m == u {
			return true
		}
	}
	return false
}

// Le processus a-t-il déjà le groupe ? (faux juste après l'ajout, avant de
// se reconnecter).
func procHasGid(gid int) bool {
	if os.Getgid() == gid {
		return true
	}
	gs, _ := os.Getgroups()
	for _, g := range gs {
		if g == gid {
			return true
		}
	}
	return false
}

// Les autres devs (membres du groupe, sauf moi).
func teammates() []string {
	var out []string
	for _, m := range lookupGroup(GroupName).members {
		if m != me && !hiddenUsers[m] {
			out = append(out, m)
		}
	}
	return out
}

// ── Petits utilitaires ──────────────────────────────────────────────────

func itoa(n int) string { return strconv.Itoa(n) }

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return itoa(n) + " " + many
}

// Durée courte : « à l'instant », « 2 min », « 3 h », « 2 j ».
func dur(since int64) string {
	if since <= 0 {
		return ""
	}
	d := time.Now().Unix() - since
	switch {
	case d < 60:
		return "à l'instant"
	case d < 3600:
		return itoa(int(d/60)) + " min"
	case d < 86400:
		return itoa(int(d/3600)) + " h"
	}
	return itoa(int(d/86400)) + " j"
}

// Date d'un commit : « 14:05 », « hier 18:42 », « 02/11 09:12 ».
func when(t int64) string {
	if t <= 0 {
		return ""
	}
	tm := time.Unix(t, 0)
	now := time.Now()
	y1, m1, d1 := tm.Date()
	y2, m2, d2 := now.Date()
	if y1 == y2 && m1 == m2 && d1 == d2 {
		if now.Sub(tm) < 90*time.Second {
			return "à l'instant"
		}
		return tm.Format("15:04")
	}
	yy, ym, yd := now.AddDate(0, 0, -1).Date()
	if y1 == yy && m1 == ym && d1 == yd {
		return "hier " + tm.Format("15:04")
	}
	return tm.Format("02/01 15:04")
}

var mois = [...]string{"janvier", "février", "mars", "avril", "mai", "juin", "juillet", "août",
	"septembre", "octobre", "novembre", "décembre"}

// Date relative longue (repos GitHub) : « il y a 3 h », « il y a 2 sem. », « en mars ».
func relDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return "il y a " + itoa(int(d.Minutes())+1) + " min"
	case d < 24*time.Hour:
		return "il y a " + itoa(int(d.Hours())) + " h"
	case d < 7*24*time.Hour:
		return "il y a " + itoa(int(d.Hours()/24)) + " j"
	case d < 31*24*time.Hour:
		return "il y a " + itoa(int(d.Hours()/24/7)) + " sem."
	}
	if t.Year() == time.Now().Year() {
		return "en " + mois[t.Month()-1]
	}
	return mois[t.Month()-1][:3] + ". " + itoa(t.Year())
}

// Nom valable pour un dossier, un repo GitHub et une session tmux.
func validName(s string) bool {
	if s == "" || len(s) > 100 || s[0] == '.' || s[0] == '-' {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.') {
			return false
		}
	}
	return true
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return s
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(s)
}

func hostname() string {
	h, _ := os.Hostname()
	return h
}
