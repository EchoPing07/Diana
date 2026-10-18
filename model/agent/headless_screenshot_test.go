// Copyright (c) 2025-now SuInk.
// Licensed under the Limited Redistribution License in the repository root.

package agent

import (
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestProbeHeadlessBrowserRenderingChecksRealScreenshot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake browser uses a POSIX shell script")
	}
	browser := filepath.Join(t.TempDir(), "chrome")
	writeExecutable(t, browser, "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'Chromium test'; fi\n")

	status := ProbeHeadlessBrowserRendering(context.Background(), browser)
	if status.Available || !strings.Contains(status.Detail, "真实截图失败") {
		t.Fatalf("status=%#v", status)
	}
}

func TestProbeHeadlessBrowserRenderingIntegration(t *testing.T) {
	if os.Getenv("DIANA_HEADLESS_BROWSER_PROBE_INTEGRATION") != "1" {
		t.Skip("set DIANA_HEADLESS_BROWSER_PROBE_INTEGRATION=1 to probe the installed browser")
	}
	status := ProbeHeadlessBrowserRendering(context.Background(), "")
	if !status.Available {
		t.Fatalf("installed browser failed real screenshot probe: %#v", status)
	}
	t.Logf("browser=%s version=%s", status.Path, status.Version)
}

func TestBrowserProbeRejectsScreenshotOnlySuccess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake browser")
	}
	dir := t.TempDir()
	fixture := filepath.Join(dir, "image.png")
	f, err := os.Create(fixture)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, image.NewRGBA(image.Rect(0, 0, 4, 4))); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	browser := filepath.Join(dir, "chromium")
	writeExecutable(t, browser, "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo 'Chromium test'; exit 0; fi\nfor arg in \"$@\"; do case \"$arg\" in --screenshot=*) cp '"+fixture+"' \"${arg#--screenshot=}\";; esac; done\n")
	status := ProbeHeadlessBrowserRendering(context.Background(), browser)
	if status.Available || !strings.Contains(status.Detail, "真实截图失败") {
		t.Fatalf("screenshot-only browser incorrectly available: %#v", status)
	}
}
