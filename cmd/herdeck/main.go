// Command herdeck shows herdr agent status on an Elgato Stream Deck Neo.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"

	"herdeck/internal/app"
	"herdeck/internal/config"
	"herdeck/internal/core"
	"herdeck/internal/deck"
	"herdeck/internal/herdr"
	"herdeck/internal/service"
	"herdeck/internal/tray"
)

// version is set by the release build (-ldflags "-X main.version=...").
var version = "dev"

const usage = `herdeck — herdr agent status on a Stream Deck Neo

Usage:
  herdeck [run] [flags]          start (menu bar app on macOS; add --headless for no tray)
  herdeck devices                list connected Stream Deck Neo decks
  herdeck doctor                 check deck, herdr and config
  herdeck config print-default   print the default config
  herdeck config path            print the config file location
  herdeck service install        start at login (launchd / systemd user unit)
  herdeck service uninstall
  herdeck version

Run flags:
`

func main() {
	log.SetFlags(log.Ltime)
	args := os.Args[1:]
	cmd := "run"
	if len(args) > 0 {
		switch args[0] {
		case "--version", "-version", "--help", "-help", "-h":
			cmd, args = strings.TrimLeft(args[0], "-"), args[1:]
		}
	}
	if cmd == "run" && len(args) > 0 && args[0] != "" && args[0][0] != '-' {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "run":
		err = runCmd(args)
	case "devices":
		err = devicesCmd()
	case "doctor":
		cfg, cerr := config.Load(config.Path())
		if !doctor(os.Stdout, cfg, cerr) {
			os.Exit(1)
		}
	case "config":
		err = configCmd(args)
	case "service":
		err = serviceCmd(args)
	case "version":
		fmt.Println("herdeck", version)
	case "help", "h":
		fmt.Print(usage)
		runFlags(&runOptions{}).PrintDefaults()
	default:
		err = fmt.Errorf("unknown command %q (see herdeck help)", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "herdeck:", err)
		os.Exit(1)
	}
}

type runOptions struct {
	config   string
	session  string
	serial   string
	headless bool
	tray     bool
	dryRun   bool
}

func runFlags(o *runOptions) *flag.FlagSet {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	fs.StringVar(&o.config, "config", config.Path(), "config file")
	fs.StringVar(&o.session, "session", "", "herdr session name (overrides config)")
	fs.StringVar(&o.serial, "serial", "", "deck serial number (overrides config)")
	fs.BoolVar(&o.headless, "headless", runtime.GOOS != "darwin", "run without the menu bar icon")
	fs.BoolVar(&o.tray, "tray", false, "force the menu bar icon (Linux: needs a StatusNotifier host)")
	fs.BoolVar(&o.dryRun, "dry-run", false, "don't open the deck; log what it would show")
	return fs
}

func runCmd(args []string) error {
	var o runOptions
	fs := runFlags(&o)
	fs.Usage = func() { fmt.Print(usage); fs.PrintDefaults() }
	fs.Parse(args)

	cfg, err := config.Load(o.config)
	if err != nil {
		return err
	}
	if o.session != "" {
		cfg.Herdr.Session = o.session
	}
	if o.serial != "" {
		cfg.Deck.Serial = o.serial
	}

	lock, err := app.AcquireLock()
	if errors.Is(err, app.ErrRunning) {
		// Exit 0 so launchd/systemd don't keep respawning a duplicate.
		fmt.Fprintln(os.Stderr, "herdeck:", err)
		return nil
	}
	if err != nil {
		return err
	}
	defer lock.Release()

	sock, err := herdr.ResolveSocket(cfg.Herdr.Session)
	if err != nil {
		return err
	}
	log.Printf("herdeck %s: herdr socket %s", version, sock)

	var conn deck.Connector = deck.NewConnector(cfg.Deck.Serial)
	if o.dryRun {
		conn = noDeck{}
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opts := app.Options{
		Config:   cfg,
		Herdr:    herdr.New(sock),
		Deck:     conn,
		Activate: app.Activator(cfg.Focus.ActivateApp),
		LogView:  o.dryRun,
	}

	if o.headless && !o.tray {
		a, err := app.New(opts)
		if err != nil {
			return err
		}
		return a.Run(ctx)
	}

	// Menu bar mode: the tray owns the main thread; the app runs beside it.
	var (
		running atomic.Pointer[app.App]
		errCh   = make(chan error, 1)
	)
	send := func(m core.Msg) {
		if a := running.Load(); a != nil {
			a.Send(m)
		}
	}
	tray.Run(tray.Callbacks{
		Focus: func(id string) { send(core.FocusRequestMsg{WorkspaceID: id}) },
		Pause: func(p bool) { send(core.PauseMsg{Paused: p}) },
		Quit:  stop,
	}, func(t *tray.Tray) {
		opts.OnTray = t.Update
		a, err := app.New(opts)
		if err != nil {
			errCh <- err
			tray.Quit()
			return
		}
		running.Store(a)
		go func() {
			errCh <- a.Run(ctx)
			tray.Quit()
		}()
	})
	stop()
	select {
	case err := <-errCh:
		return err
	default:
		return nil
	}
}

// noDeck is the --dry-run connector: never connects.
type noDeck struct{}

func (noDeck) Run(ctx context.Context) <-chan deck.Event {
	ch := make(chan deck.Event)
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return ch
}

func devicesCmd() error {
	infos, err := deck.List()
	if err != nil {
		return err
	}
	if len(infos) == 0 {
		return deck.ErrNotFound
	}
	for _, d := range infos {
		state := "firmware " + d.Firmware
		if d.Busy {
			state = "busy (in use by another app)"
		}
		fmt.Printf("%s  %s  %s\n", d.Serial, d.Model, state)
	}
	return nil
}

func configCmd(args []string) error {
	if len(args) == 1 {
		switch args[0] {
		case "print-default":
			fmt.Print(config.DefaultTOML())
			return nil
		case "path":
			fmt.Println(config.Path())
			return nil
		}
	}
	return errors.New("usage: herdeck config print-default|path")
}

func serviceCmd(args []string) error {
	if len(args) == 1 {
		switch args[0] {
		case "install":
			p, err := service.Install()
			if err == nil {
				fmt.Println("installed", p)
				if runtime.GOOS == "darwin" {
					fmt.Println("logs:", service.LogPath())
				}
			}
			return err
		case "uninstall":
			return service.Uninstall()
		}
	}
	return errors.New("usage: herdeck service install|uninstall")
}
