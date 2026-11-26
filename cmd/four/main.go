package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

const version = "0.2.0"

const helpText = `four : le menu des projets du serveur.

  four              ouvre le menu
  four <projet>     ouvre directement le projet (dernière session, sinon Claude)
  four setup        assistant : clé SSH, GitHub, identité git, Claude, notifications,
                    Codex et Gemini (facultatifs)
  four version

Dans le menu : n lance une session : Claude, Codex ou Gemini au choix.
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
			if len(args) > 2 && args[2] == agentAgy {
				fmt.Println("{}") // Antigravity attend une réponse JSON
			}
		}
	case "_status":
		fmt.Print(statusLine(args[1:]))
	case "_statusline":
		statusLineCmd(os.Stdin)
	case "_panel":
		runPanel()
	case "_panel-ensure":
		panelEnsure(argAt(args, 1))
	case "_panel-toggle":
		panelToggle(argAt(args, 1))
	case "_panel-narrow":
		panelNarrowToggle(argAt(args, 1))
	case "_goto":
		n, _ := strconv.Atoi(argAt(args, 1))
		panelGoto(n, argAt(args, 2), argAt(args, 3))
	case "_next":
		panelNext(argAt(args, 1), argAt(args, 2))
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

func argAt(args []string, i int) string {
	if i < len(args) {
		return args[i]
	}
	return ""
}
