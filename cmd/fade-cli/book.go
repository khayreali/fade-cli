package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"fadecli/internal/provider"
	"fadecli/internal/ui"
)

func (a *app) book(args []string) error {
	fs := flag.NewFlagSet("book", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `fade-cli book <shop> [flags]

Open a shop's booking system, or dial it if they only take calls.

FLAGS
`)
		fs.PrintDefaults()
	}
	var (
		yes   = fs.Bool("y", false, "skip the confirmation prompt")
		print = fs.Bool("print", false, "print the target instead of opening it")
	)
	if err := parse(fs, args); err != nil {
		return nil
	}

	name := strings.Join(fs.Args(), " ")
	if name == "" {
		fs.Usage()
		return errors.New("name a shop")
	}

	shop, err := a.mustResolve(name)
	if err != nil {
		return err
	}
	action := a.reg.For(shop).Handoff(shop)

	fmt.Println()
	fmt.Printf("  %s\n", ui.Bold(shop.Name))
	fmt.Printf("  %s\n", ui.Dim(shop.Address))
	if shop.Rating > 0 {
		fmt.Printf("  %s   %s\n", ui.Stars(shop.Rating, shop.Reviews), priceCell(shop))
	}
	fmt.Println()

	switch action.Type {
	case provider.ActionNone:
		return fmt.Errorf("%s: %s", shop.Name, action.Label)
	case provider.ActionCall:
		fmt.Printf("  %s %s\n", ui.Dim("call"), ui.Bold(provider.PrettyPhone(shop.Phone)))
	case provider.ActionOpen:
		fmt.Printf("  %s %s\n", ui.Dim("open"), action.Target)
	}
	fmt.Println()

	if *print {
		fmt.Println(action.Target)
		return nil
	}

	if !*yes && !confirm(fmt.Sprintf("  %s? [Y/n] ", action.Label)) {
		fmt.Println(ui.Dim("  cancelled"))
		return nil
	}

	if err := provider.Open(action.Target); err != nil {
		return fmt.Errorf("opening %s: %w", action.Target, err)
	}
	ui.Hint("after your cut: fade-cli log add --shop %s --price <$>", shop.ID)
	return nil
}

// confirm defaults to yes, since the user already typed the shop name. A bare
// enter should do the obvious thing. Non-interactive input declines rather
// than opening a browser nobody asked for.
func confirm(prompt string) bool {
	fi, err := os.Stdin.Stat()
	if err != nil || fi.Mode()&os.ModeCharDevice == 0 {
		return false
	}
	fmt.Print(prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "y", "yes":
		return true
	default:
		return false
	}
}
