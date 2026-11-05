package main

import (
	"fmt"
	"os"
	"strings"
)

const version = "0.1.1"

const helpText = `four : le menu des projets du serveur.

  four              ouvre le menu
  four <projet>     ouvre directement le projet (dernière session, sinon Claude)
  four setup        assistant : clé SSH, GitHub, identité git, Claude, notifications
  four version

Dans une session : Ctrl-b Espace ouvre le menu par-dessus, Ctrl-b d revient
au menu, Ctrl-b x ferme la session. Projets : ` + "%s" + `
`

func main() {
	args := os.Args[1:]
	if len(args) == 0 || !strings.HasPrefix(args[0], "_") {
		regainGroup()
	}
	if len(args) == 0 {
		runTUI(scrDash, false, "")
		return
	}
	switch args[0] {
	case "_run":
		os.Exit(runSession())
	case "_notify":
		if len(args) > 1 {
			notify(args[1])
		}
	case "_status":
		fmt.Print(statusLine(args[1:]))
	case "_statusline":
		statusLineCmd(os.Stdin)
	case "--popup":
		runTUI(scrDash, true, "")
	case "setup", "config":
		runTUI(scrSetup, false, "")
	case "version", "--version", "-v":
		fmt.Println("four", version)
	case "help", "--help", "-h":
		fmt.Printf(helpText, Root)
	default:
		if len(args[0]) > 0 && args[0][0] == '-' {
			fmt.Fprintf(os.Stderr, "four : option inconnue %s\n\n", args[0])
			fmt.Fprintf(os.Stderr, helpText, Root)
			os.Exit(2)
		}
		runTUI(scrDash, false, args[0])
	}
}
