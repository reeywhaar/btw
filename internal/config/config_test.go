package config

import (
	"strings"
	"testing"
	"time"
)

func TestBackupsAreOffWithNoAgent(t *testing.T) {
	t.Setenv(PublicURLEnv, "https://btw.example.com")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	// Nowhere to post is the whole of the switch: nothing to configure, nothing running.
	if cfg.BackupURL != "" {
		t.Errorf("backups are on with nothing set: %q", cfg.BackupURL)
	}
}

func TestNamingAnAgentTurnsBackupsOn(t *testing.T) {
	t.Setenv(PublicURLEnv, "https://btw.example.com")
	t.Setenv(BackupURLEnv, "http://backup:8080/backup")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.BackupURL != "http://backup:8080/backup" {
		t.Errorf("BackupURL is %q", cfg.BackupURL)
	}
	if cfg.BackupMode != DefaultBackupMode {
		t.Errorf("BackupMode is %q, want %q", cfg.BackupMode, DefaultBackupMode)
	}
}

func TestTheModeCanBeChosen(t *testing.T) {
	for _, mode := range []BackupMode{BackupMain, BackupRelaxed, BackupAll} {
		t.Run(string(mode), func(t *testing.T) {
			t.Setenv(PublicURLEnv, "https://btw.example.com")
			t.Setenv(BackupURLEnv, "http://backup:8080/backup")
			t.Setenv(BackupModeEnv, string(mode))
			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load(): %v", err)
			}
			if cfg.BackupMode != mode {
				t.Errorf("BackupMode is %q, want %q", cfg.BackupMode, mode)
			}
		})
	}
}

func TestAModeIsCaseInsensitive(t *testing.T) {
	t.Setenv(PublicURLEnv, "https://btw.example.com")
	t.Setenv(BackupURLEnv, "http://backup:8080/backup")
	t.Setenv(BackupModeEnv, "All")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}
	if cfg.BackupMode != BackupAll {
		t.Errorf("BackupMode is %q, want %q", cfg.BackupMode, BackupAll)
	}
}

func TestAModeThatIsNotOneIsRefused(t *testing.T) {
	t.Setenv(PublicURLEnv, "https://btw.example.com")
	t.Setenv(BackupURLEnv, "http://backup:8080/backup")
	t.Setenv(BackupModeEnv, "everything")
	_, err := Load()
	if err == nil {
		t.Fatal("Load() accepted a mode that is not one")
	}
	// The message has to list them: there is nowhere else an operator would look.
	for _, mode := range []BackupMode{BackupMain, BackupRelaxed, BackupAll} {
		if !strings.Contains(err.Error(), string(mode)) {
			t.Errorf("the message does not offer %q: %v", mode, err)
		}
	}
}

func TestAPolicyForArchivesNobodySendsIsRefused(t *testing.T) {
	// Believing backups are on is a mistake that stays invisible until somebody needs one, so
	// a setting that only makes sense alongside an agent refuses to start without one.
	t.Setenv(PublicURLEnv, "https://btw.example.com")
	t.Setenv(BackupModeEnv, string(BackupAll))
	_, err := Load()
	if err == nil {
		t.Fatalf("Load() accepted %s with no %s", BackupModeEnv, BackupURLEnv)
	}
	if !strings.Contains(err.Error(), BackupURLEnv) {
		t.Errorf("the message does not name what is missing: %v", err)
	}
}

func TestWhatEachModePromises(t *testing.T) {
	for _, tc := range []struct {
		mode    BackupMode
		derived bool
		period  time.Duration
	}{
		{BackupMain, false, 0},
		{BackupRelaxed, true, 0},
		{BackupAll, true, BackupAllPeriod},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			if got := tc.mode.Derived(); got != tc.derived {
				t.Errorf("Derived() is %v, want %v", got, tc.derived)
			}
			// Only "all" sends when nothing was typed; the others would be a heartbeat
			// nobody asked for.
			if got := tc.mode.Period(); got != tc.period {
				t.Errorf("Period() is %s, want %s", got, tc.period)
			}
		})
	}
}

func TestAnAgentURLMustBeOne(t *testing.T) {
	for _, raw := range []string{"backup:8080/backup", "/backup", "ftp://backup/backup", "not a url"} {
		t.Run(raw, func(t *testing.T) {
			t.Setenv(PublicURLEnv, "https://btw.example.com")
			t.Setenv(BackupURLEnv, raw)
			// A scheme-less host is the likely typo, and left alone it would fail once an
			// interval in a log nobody is reading.
			if _, err := Load(); err == nil {
				t.Errorf("Load() accepted %q as an agent URL", raw)
			}
		})
	}
}
