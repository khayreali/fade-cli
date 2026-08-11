// Command fade-cli finds and books haircuts from the terminal.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	_ "time/tzdata" // shop hours are evaluated in New York time

	"fadecli/internal/catalog"
	"fadecli/internal/provider"
	"fadecli/internal/store"
	"fadecli/internal/ui"
)

const version = "0.1.0"

// app carries the wiring every command needs, so each command body can stay
// about its own job.
type app struct {
	cat   *catalog.Catalog
	state *store.State
	reg   *provider.Registry
}

func main() {
	// No arguments at a terminal means "just show me haircuts" -- the flags
	// are there for people who want them, not a prerequisite for using this.
	if len(os.Args) < 2 {
		if !ui.IsInteractive() {
			usage()
			os.Exit(2)
		}
		a, err := newApp()
		if err != nil {
			ui.Errf("%v", err)
			os.Exit(1)
		}
		if err := a.interactive(); err != nil && !errors.Is(err, errAborted) {
			ui.Errf("%v", err)
			os.Exit(1)
		}
		return
	}

	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "-h", "--help", "help":
		usage()
		return
	case "-v", "--version", "version":
		fmt.Println("fade cli " + version)
		return
	}

	a, err := newApp()
	if err != nil {
		ui.Errf("%v", err)
		os.Exit(1)
	}

	var runErr error
	switch cmd {
	case "find", "f":
		runErr = a.find(args)
	case "slots", "s":
		runErr = a.slots(args)
	case "book", "b":
		runErr = a.book(args)
	case "again":
		runErr = a.againCmd(args)
	case "appts", "appointments":
		runErr = a.appts(args)
	case "log", "l":
		runErr = a.log(args)
	case "due":
		runErr = a.due(args)
	case "me":
		runErr = a.me(args)
	case "stops":
		runErr = a.stops(args)
	case "dev":
		runErr = a.dev(args)
	default:
		ui.Errf("unknown command %q", cmd)
		usage()
		os.Exit(2)
	}

	if runErr != nil && !errors.Is(runErr, errAborted) {
		ui.Errf("%v", runErr)
		os.Exit(1)
	}
}

func newApp() (*app, error) {
	st, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("loading your settings: %w", err)
	}
	cat, err := catalog.Load(st.LocalCatalogPath())
	if err != nil {
		return nil, err
	}
	return &app{cat: cat, state: st, reg: provider.NewRegistry(os.Getenv)}, nil
}

// parse accepts flags before or after positional arguments. Go's flag package
// stops at the first non-flag token, so `fade-cli book "power of barbers" --print`
// would otherwise swallow --print as part of the shop name -- and that word
// order is the one people actually type.
func parse(fs *flag.FlagSet, args []string) error {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			positional = append(positional, a)
			continue
		}

		flags = append(flags, a)
		if strings.Contains(a, "=") {
			continue // --key=value carries its own value
		}
		name := strings.TrimLeft(a, "-")
		if f := fs.Lookup(name); f != nil && !isBoolFlag(f) && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	// Re-insert the terminator so anything we classified as positional stays
	// positional -- otherwise a shop named like a flag gets re-parsed as one.
	return fs.Parse(append(append(flags, "--"), positional...))
}

func isBoolFlag(f *flag.Flag) bool {
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

func usage() {
	fmt.Print(`fade cli -- book a haircut from the command line

USAGE
  fade-cli                 browse shops near you and book (start here)
  fade-cli <command>       [flags]

COMMANDS
  again     rebook wherever you went last
  appts     booking requests made through the manual connectors
  find      search shops by distance, price, rating
  slots     check open appointment times
  book      book, or hand off to the shop's booking system
  log       record a haircut, or review your history
  due       when you're next due for a cut
  me        view or set your profile
  stops     list the stops in the service area
  dev       maintenance tasks (geocode, check)

EXAMPLES
  fade-cli                            browse everything near you
  fade-cli again                      rebook your last shop
  fade-cli find --under 45 --min-rating 4.9
  fade-cli find --to montrose --collapse
  fade-cli slots cabello --day tomorrow
  fade-cli book "power of barbers"
  fade-cli book eddo --at "fri 3pm"       request a time by call or text
  fade-cli log add --shop cabello --price 55 --tip 10

Run 'fade-cli <command> -h' for flags.
`)
}
