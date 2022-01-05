package main

// Notifies the user via desktop notifications
// by sending DBus messages.
// FIXME: Currently it does not crash but it does not work either
// when other things using DBus are already running (e.g, watching UDisks)

import (
	"log"
	"os"
	"sync"
	"time"

	"github.com/esiqveland/notify"
	"github.com/godbus/dbus/v5"
	"github.com/sagesound/go-appimage/internal/helpers"
)

// sendUpdateDesktopNotification sends a desktop notification for an update.
// Use this with "go" prefixed to it so that it runs in the background, because it waits
// until the user clicks on "Update" or the timeout occurs
func sendUpdateDesktopNotification(ai *AppImage, version string, _ string) {

	wg := &sync.WaitGroup{}

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

	// Create a Notification to send
	iconName := "software-update-available"
	n := notify.Notification{
		AppName:       ai.Name,
		ReplacesID:    uint32(0),
		AppIcon:       iconName,
		Summary:       "Update available",
		Body:          ai.Name + " can be updated to version " + version + ". \nchangelog",
		Actions:       []notify.Action{{Key: "update", Label: "Update"}}, // tuples of (action_key, label)
		Hints:         map[string]dbus.Variant{},
		ExpireTimeout: time.Duration(time.Minute * 2),
	}
	// List server capabilities
	caps, err := notify.GetCapabilities(conn)
	if err != nil {
		log.Printf("error fetching capabilities: %v", err)
	}
	for x := range caps {
		log.Printf("Registered capability: %v\n", caps[x])
	}

	// TODO: Only send fancy notification if server has the capabilities for it,
	// otherwise fall back to simple sendDesktopNotification()

	info, err := notify.GetServerInformation(conn)
	if err != nil {
		log.Printf("error getting server information: %v", err)
	}
	log.Printf("Name:    %v\n", info.Name)
	log.Printf("Vendor:  %v\n", info.Vendor)
	log.Printf("Version: %v\n", info.Version)
	log.Printf("Spec:    %v\n", info.SpecVersion)

	var memory = map[uint32]*notify.Notification{} // https://github.com/esiqveland/notify/issues/8#issuecomment-584881627

	// Listen for actions invoked
	onAction := func(action *notify.ActionInvokedSignal) {
		if action != nil { // Without this if we get a crash if user just closes the notification w/o an action
			log.Printf("ActionInvoked: %v Key: %v", action.ID, action.ActionKey)
			// Check based on &n == memory[action.ID] whether this onAction belongs to the notification we sent,
			// Only act on notifications with "our" action ID
			// https://github.com/esiqveland/notify/issues/8#issuecomment-584881627
			if action.ActionKey == "update" && &n == memory[action.ID] {
				log.Println("runUpdate", ai.Path)
				runUpdate(ai.Path)
			}
		}
		wg.Done()
	}

	onClosed := func(closer *notify.NotificationClosedSignal) {
		log.Printf("NotificationClosed: %v Reason: %v", closer.ID, closer.Reason)
	}

	// Notifier interface with event delivery
	notifier, err := notify.New(
		conn,
		// action event handler
		notify.WithOnAction(onAction),
		// closed event handler
		notify.WithOnClosed(onClosed),
		// override with custom logger
		notify.WithLogger(log.New(os.Stdout, "notify: ", log.Flags())),
	)
	if err != nil {
		log.Fatalln(err.Error())
	}
	defer notifier.Close()

	id, err := notifier.SendNotification(n)
	if err != nil {
		log.Printf("error sending notification: %v", err)
	} else {
		memory[id] = &n
		log.Printf("sent notification id: %v", id)
	}

	wg.Add(2)
	wg.Wait()
}

func sendDesktopNotification(title string, body string, durationms int32) {

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

	log.Println("Desktop notification: ", title, body)

	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	if obj == nil {
		log.Println("ERROR: notification: obj is nil")
		return
	}

	call := obj.Call("org.freedesktop.Notifications.Notify", 0, "", uint32(0),
		"", title, body, []string{},
		map[string]dbus.Variant{}, durationms)

	if call.Err != nil {
		log.Println("ERROR: notification:", call.Err)
		// Sometimes we get here: "read unix @->/run/user/999/bus: EOF"
		// means that we are not using PrivateConnection?
		log.Println("Is a notification daemon installed, and are you running on a supported system?")
		return
	}

}
