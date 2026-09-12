package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"syscall"
	"time"

	"fadecli/internal/ui"
	"fadecli/internal/update"
)

// updateCmd works even if the user's profile or catalog fails to load.
func updateCmd(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	check := fs.Bool("check", false, "check for a release without installing it")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: fade-cli update [--check]")
	}
	fmt.Printf("fade cli %s · checking for updates...\n", version)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	client := update.Client{}
	r, err := client.Latest(ctx)
	if errors.Is(err, update.ErrNoRelease) {
		fmt.Println("No stable release is published yet.")
		return nil
	}
	if err != nil {
		return err
	}
	if !r.Newer(version) {
		fmt.Printf("You're up to date (%s).\n", version)
		return nil
	}
	fmt.Printf("Update available: %s -> %s\n%s\n", version, r.Version(), r.Page())
	if *check {
		fmt.Println("Run fade-cli update to install it.")
		return nil
	}
	path, err := update.Executable()
	if err != nil {
		return err
	}
	fmt.Printf("Installing %s...\n", path)
	if err = client.Install(ctx, r, path); err != nil {
		return err
	}
	fmt.Printf("Updated to %s. Run fade-cli to start the new version.\n", r.Version())
	return nil
}

func updateInteractive(raw *ui.Raw) error {
	var r update.Release
	client := update.Client{}
	err := bookingWait(raw, "Checking for updates", func(ctx context.Context) error {
		var err error
		r, err = client.Latest(ctx)
		return err
	})
	if errors.Is(err, errAborted) || errors.Is(err, context.Canceled) {
		return bookingExit(err)
	}
	message := "You're up to date."
	rows := [][]string{{"Back"}}
	if err != nil {
		message = "Could not check for updates. Try again later."
	}
	if errors.Is(err, update.ErrNoRelease) {
		message = "No stable release is published yet."
	}
	available := err == nil && r.Newer(version)
	if available {
		message = fmt.Sprintf("%s -> %s\nThe app will restart after installing the update.", version, r.Version())
		rows = [][]string{{ui.Accent("Install & restart")}, {"Later"}}
	}
	for {
		list := &ui.List{Title: "fade cli updates", Subtitle: "Installed: " + version, Hint: message, Rows: rows, Height: len(rows), Hints: []ui.KeyHint{{Key: ui.EscKey, Label: "back"}, {Key: "q", Label: "quit"}}}
		got := list.Run(raw, 0)
		if !got.OK || got.Cmd == "q" {
			return errAborted
		}
		if got.Cmd != "" || !available || got.Index != 0 {
			return nil
		}
		path, pathErr := update.Executable()
		if pathErr != nil {
			message = pathErr.Error()
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		installErr := raw.Wait(ctx, "Installing fade cli "+r.Version(), func(ctx context.Context) error { return client.Install(ctx, r, path) })
		cancel()
		if errors.Is(installErr, ui.ErrInterrupted) {
			return errAborted
		}
		if errors.Is(installErr, context.Canceled) {
			return nil
		}
		if installErr != nil {
			message = "Update failed: " + installErr.Error()
			continue
		}
		raw.Restore()
		return syscall.Exec(path, []string{path}, os.Environ())
	}
}

func updateNotice(list *ui.List, r update.Release) {
	if r.Newer(version) {
		list.Hint = ui.Accent("Update available: "+version+" -> "+r.Version()) + "\nPress u to install and restart, or keep browsing."
	}
}
