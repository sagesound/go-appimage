// appwrapper executes applications and presents errors to the GUI as notifications
// TODO: Enable appimaged for DBus Activation so that the running instance can wrap
// the apps, so that we don't need to run another appimaged process for each app
package main

import (
	"bytes"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/adrg/xdg"
	"github.com/godbus/dbus/v5"
	"github.com/sagesound/go-appimage/internal/helpers"
	"gopkg.in/ini.v1"
)

func appwrap() {

	if len(os.Args) < 3 {
		log.Println("Argument missing")
		os.Exit(1)
	}

	cmd := exec.Command(os.Args[2], os.Args[3:]...)

	var out bytes.Buffer
	cmd.Stderr = &out

	// Find desktop file(s) that point to the executable in os.Args[2],
	// and check them with desktop-file-verify; display notification if verification fails
	go checkDesktopFiles(os.Args[2])

	ai, err := NewAppImage(os.Args[2])

	if err == nil {
		// TODO: If we have an AppImage, then check the updateinformation inside the AppImage (or better: lint the AppImage)
		err := ai.Validate()
		if err != nil {
			sendDesktopNotification(ai.Name+" is not a proper AppImage", err.Error()+"\nPlease ask the author to fix it.", 30000)
		}
		// TODO: If we have an AppImage, then check the desktop file inside the AppImage (or better: lint the AppDir, reuse code from appimagetool)
		// TODO: If we have an AppImage, then check that the .DirIcon  inside the AppImage exists (or better: lint the AppDir, reuse code from appimagetool)
	}

	if err := cmd.Start(); err != nil {
		log.Fatalf("cmd.Start: %v", err)
	}

	if err := cmd.Wait(); err != nil {
		if exiterr, ok := err.(*exec.ExitError); ok {
			// The program has exited with an exit code != 0
			if status, ok := exiterr.Sys().(syscall.WaitStatus); ok {
				log.Printf("Exit Status: %d", status.ExitStatus())
				log.Println(out.String())

				// If what we launched (and failed) was an AppImage, then use its nice (short) name
				// to display the error message
				var appname string
				ai, err = NewAppImage(os.Args[2])
				if err == nil {
					appname = ai.Name
				} else {
					appname = filepath.Base(os.Args[2])
				}

				summary := "Cannot open " + appname
				body := strings.TrimSpace(out.String())

				if strings.Contains(out.String(), "cannot open shared object file: No such file or directory") {
					parts := strings.Split(out.String(), ":")
					body = "Missing library " + strings.TrimSpace(parts[2])
					// summary = "Error: Missing library " + strings.TrimSpace(parts[2])
					// body = filepath.Base(os.Args[2]) + " could not be started because " + strings.TrimSpace(parts[2]) + " is missing"
				}

				// https://github.com/AppImage/AppImageKit/issues/1004
				if strings.Contains(out.String(), "execv error") && err == nil {
					body = filepath.Base(os.Args[2]) + " is defective, AppRun is missing. \nPlease ask the author to fix it."
				}

				// https://github.com/pinnaculum/galacteek/issues/6
				if strings.Contains(out.String(), "Could not load the Qt platform plugin") && err == nil {
					body = filepath.Base(os.Args[2]) + " is defective, could not load the Qt platform plugin. \nPlease run on the command line with 'QT_DEBUG_PLUGINS=1' \nto see error messages and ask the author to fix it."
				}

				sendErrorDesktopNotification(summary, body)
			}
		} else {
			log.Fatalf("cmd.Wait: %v", err)
		}
	}
}

// Send desktop notification. See
// https://developer.gnome.org/notification-spec/
func sendErrorDesktopNotification(title string, body string) {
	log.Println("----------------------------")
	log.Println("Notification:")
	log.Println(title)
	log.Println(body)

	conn, err := dbus.SessionBusPrivate() // When using SessionBusPrivate(), need to follow with Auth(nil) and Hello()
	if err != nil {
		if conn != nil {
			conn.Close()
		}
		helpers.PrintError("SessionBusPrivate", err)
		return
	}
	defer conn.Close()
	if conn == nil {
		helpers.PrintError("No conn", err)
		return
	}

	if err = conn.Auth(nil); err != nil {
		helpers.PrintError("Auth", err)
		return
	}

	if err = conn.Hello(); err != nil {
		helpers.PrintError("Hello", err)
		return
	}

	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.Call("org.freedesktop.Notifications.Notify", 0, "", uint32(0),
		"", title, body, []string{},
		map[string]dbus.Variant{},
		int32(0)) // The timeout time in milliseconds at which the notification should automatically close.
	// If -1, the notification's expiration time is dependent on the notification server's settings,
	// and may vary for the type of notification.
	// If 0, the notification never expires.

	if call.Err != nil {
		log.Println("ERROR: appwrapper:", call.Err)
	}
}

// findDesktopFilesPointingToExecutable returns those desktop files
// which have Exec= entries pointing to the executable
func findDesktopFilesPointingToExecutable(executablefilepath string) ([]string, error) {
	var results []string
	files, e := ioutil.ReadDir(xdg.DataHome + "/applications/")
	helpers.LogError("desktop", e)
	if e != nil {
		return results, e
	}

	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".desktop") {
			cfg, _ := ini.LoadSources(ini.LoadOptions{IgnoreInlineComment: true}, // Do not cripple lines hat contain ";"
				xdg.DataHome+"/applications/"+file.Name())
			// log.Println(xdg.DataHome + "/applications/" + file.Name())
			s := cfg.Section("Desktop Entry").Key("Exec").String()
			// dst = strings.Replace(dst, os.Args[0]+" "+os.Args[1]+" ", "", -1)
			// log.Println(s)
			if strings.Contains(s, executablefilepath) {
				results = append(results, file.Name())
			}

		}
	}
	return results, nil
}

func checkDesktopFiles(executablefilepath string) {
	// log.Println(executablefilepath)
	dfiles, err := findDesktopFilesPointingToExecutable(executablefilepath)
	// log.Println(dfiles)
	helpers.PrintError("checkDesktopFiles", err)
	for _, dfile := range dfiles {
		// log.Println(dfile)
		err := helpers.ValidateDesktopFile(xdg.DataHome + "/applications/" + dfile)
		if err != nil {
			sendErrorDesktopNotification("Invalid desktop file", executablefilepath+"\n\n"+err.Error())
		}
	}
}
