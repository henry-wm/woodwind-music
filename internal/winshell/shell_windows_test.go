//go:build windows

package winshell

import (
	"fmt"
	"os"
	"testing"
	"time"
	"unsafe"
)

func TestNativeTrayRegistration(t *testing.T) {
	if os.Getenv("WOOD_WIND_WINDOWS_NATIVE") != "1" {
		t.Skip("set WOOD_WIND_WINDOWS_NATIVE=1 on a signed-in Windows desktop")
	}
	if err := Start("dev.oneo.nsgo.music.test"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(Stop)
	ConfigureTray("Woodwind test", func() {}, func() {})
	result := make(chan error, 1)
	submit(func(s *shell) {
		if !TrayRegistered() {
			result <- fmt.Errorf("tray icon registration failed")
			return
		}
		// Explorer's restart drops the icon without destroying our process.
		notifyIcon.Call(2, uintptr(unsafe.Pointer(&s.tray)))
		shellProc(s.hwnd, s.taskbarCreated, 0, 0)
		if !TrayRegistered() {
			result <- fmt.Errorf("Explorer restart did not recreate the tray icon")
			return
		}
		s.removeTray()
		if TrayRegistered() {
			result <- fmt.Errorf("tray registration remained after removal")
			return
		}
		result <- nil
	})
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("native shell did not respond")
	}
}
