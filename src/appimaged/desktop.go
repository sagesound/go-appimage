package main

// Handles reading, writing, installing, and verifying desktop files.
// Currently it is using using a static build of desktop-file-validate
// but eventually may be rewritten to do things natively in Go.

import (
	"bufio"
	"bytes"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/adrg/xdg"
	"github.com/sagesound/go-appimage/internal/helpers"
	"gopkg.in/ini.v1"
)

// Write desktop file for a given AppImage to a temporary location.
// Call this with "go" because we have intentional delay in here (we are waiting for
// external thumbnailers to complete), which means it does not return
// for a while
func writeDesktopFile(ai AppImage) {

	filename := "appimagekit_" + ai.md5 + ".desktop"

	// log.Println(md5s)
	// XDG directories
	// log.Println(xdg.DataHome)
	// log.Println(xdg.DataDirs)
	// log.Println(xdg.ConfigHome)
	// log.Println(xdg.ConfigDirs)
	desktopcachedir := xdg.CacheHome + "/applications/" // FIXME: Do not hardcode here and in other places

	err := os.MkdirAll(desktopcachedir, os.ModePerm)
	if err != nil {
		log.Printf("desktop: %v", err)
	}
	// log.Println(xdg.RuntimeDir)
	var cfg *ini.File
	ini.PrettyFormat = false
	startingPoint := false //An easy way to tell if extracting the desktop file worked.
	arg0abs, err := filepath.Abs(os.Args[0])

	// FIXME: KDE seems to have a problem when the AppImage is on a partition of which the disklabel contains "_"?
	// Then the desktop file won't run the application
	if err != nil {
		log.Println(err)
	}
	if ai.Desktop != nil {
		//Start with a fresh copy of the desktop file so we don't make edits to ai.Desktop

		desktopRdr, _ := ai.ExtractFileReader("*.desktop")
		defer desktopRdr.Close()
		//cleaning the desktop file so it can be parsed properly
		var desktop []byte
		buf := bufio.NewReader(desktopRdr)
		for err == nil {
			var line string
			line, err = buf.ReadString('\n')
			if strings.Contains(line, ";") {
				line = strings.ReplaceAll(line, ";", "；") //replacing it with a fullwidth semicolon (unicode FF1B)
			}
			desktop = append(desktop, line...)
		}
		cfg, err = ini.Load(desktop)
		if err == nil {
			startingPoint = true
		}
		//TODO: check if the thumbnail is already present and only extract it and set it's value if it isn't
	}

	if !startingPoint {
		cfg = ini.Empty()
		cfg.Section("Desktop Entry").Key("Type").SetValue("Application")
		cfg.Section("Desktop Entry").Key("Name").SetValue(ai.Name)
	} else {
		if !cfg.Section("Desktop Entry").HasKey("Name") {
			cfg.Section("Desktop Entry").Key("Name").SetValue(ai.Name)
		}
		if !cfg.Section("Desktop Entry").HasKey("Type") {
			cfg.Section("Desktop Entry").Key("Type").SetValue("Application")
		}
	}
	thumbnail := ThumbnailsDirNormal + ai.md5 + ".png"
	cfg.Section("Desktop Entry").Key("Icon").SetValue(thumbnail)
	// Construct the Name entry based on the actual filename
	// so that renaming the file in the file manager results in a changed name in the menu
	// FIXME: If the thumbnail is not generated here but by another external thumbnailer, it may not be fast enough
	time.Sleep(1 * time.Second)
	cfg.Section("Desktop Entry").Key("Exec").SetValue(arg0abs + " wrap \"" + ai.Path + "\"") // Resolve to a full path
	cfg.Section("Desktop Entry").Key(ExecLocationKey).SetValue(ai.Path)
	cfg.Section("Desktop Entry").Key("TryExec").SetValue(arg0abs) // Resolve to a full path
	// For icons, use absolute paths. This way icons start working
	// without having to restart the desktop, and possibly
	// we can even get around messing around with the XDG icon spec
	// that expects different sizes of icons in different directories
	/*
		if _, err := os.Stat(thumbnail); err == nil {
			// Thumbnail exists, then we use it as the Icon in the desktop file
			// TODO: Maybe we should assume the icon exists; and only thereafter "go extract it" for performance
			// so that we get the menu entries even if the icons have not been extracted yet
			cfg.Section("Desktop Entry").Key("Icon").SetValue(thumbnail)
		} else if os.IsNotExist(err) {
			// Thumbnail  does *not* exist, then we use a default application icon (better than nothing)
			cfg.Section("Desktop Entry").Key("Icon").SetValue("application-default-icon") // Use this if no other one is set or it is not found
			// TODO: Move away from here. Make one image struct regardless of type?

		}
	*/
	cfg.Section("Desktop Entry").Key("Comment").SetValue(ai.Path)
	cfg.Section("Desktop Entry").Key("X-AppImage-Identifier").SetValue(ai.md5)
	ui := ai.updateinformation
	if ui != "" {
		cfg.Section("Desktop Entry").Key(helpers.UpdateInformationKey).SetValue("\"" + ui + "\"")
	}
	// Actions

	var actions []string

	if isWritable(ai.Path) {
		// Add "Move to Trash" action
		// if the AppImage is writeable (= the user can remove it)
		//
		// FIXME: The current implementation is desktop specfific and breaks
		// if the user uses the same home directory with multiple desktops.
		// Why isn't there a XDG standard tool or dbus call to move files to the Trash?
		// According to http://xahlee.info/linux/linux_trash_location.html:
		// Where is the trash directory?
		// ~/.local/share/Trash/ → on your local file system.
		// /root/.local/share/Trash/ → if you are root, on your local file system.
		// /media/PENDRIVE/.Trash-1000/ → on a USB drive.
		actions = append(actions, "Trash")
		cfg.Section("Desktop Action Trash").Key("Name").SetValue("Move to Trash")
		if helpers.IsCommandAvailable("gio") {
			// A command line tool to move files to the Trash. However, GNOME-specific
			cfg.Section("Desktop Action Trash").Key("Exec").SetValue("gio trash \"" + ai.Path + "\"")
		} else if helpers.IsCommandAvailable("kioclient") {
			// Of course KDE has its own facility for doing the exact same thing
			cfg.Section("Desktop Action Trash").Key("Exec").SetValue("kioclient move \"" + ai.Path + "\" trash:/")
		} else {
			// Provide a fallback shell command to prevent parser errors on other desktops
			cfg.Section("Desktop Action Trash").Key("Exec").SetValue("mv \"" + ai.Path + "\" ~/.local/share/Trash/")
		}

		// Add OpenPortableHome action
		actions = append(actions, "OpenPortableHome")
		cfg.Section("Desktop Action OpenPortableHome").Key("Name").SetValue("Open Portable Home in File Manager")
		cfg.Section("Desktop Action OpenPortableHome").Key("Exec").SetValue("xdg-open \"" + ai.Path + ".home\"")

		// Add CreatePortableHome action
		actions = append(actions, "CreatePortableHome")
		cfg.Section("Desktop Action CreatePortableHome").Key("Name").SetValue("Create Portable Home")
		cfg.Section("Desktop Action CreatePortableHome").Key("Exec").SetValue("mkdir -p \"" + ai.Path + ".home\"")

	}

	// Add OpenDesktopFile action
	// actions = append(actions, "OpenDesktopFile")
	// cfg.Section("Desktop Action OpenDesktopFile").Key("Name").SetValue("Open Desktop File")
	// FIXME: This would actually launch the desktop file, not show it in an editor!
	// cfg.Section("Desktop Action OpenDesktopFile").Key("Exec").SetValue("xdg-open '" + ai.desktopfilepath + "'")

	// Add "Extract" action
	// TODO: Actually, we could do the extraction ourselves since we have the extraction logic on board anyways
	// then we could have a better name for the extracted location, and could handle type-1 as well
	// TODO: Maybe have a dbus action for extracting AppImages that could be invoked?
	if ai.Type() > 1 {
		actions = append(actions, "Extract")
		cfg.Section("Desktop Action Extract").Key("Name").SetValue("Extract to AppDir")
		if isWritable(ai.Path) {
			cfg.Section("Desktop Action Extract").Key("Exec").SetValue("bash -c \"cd '" + filepath.Clean(ai.Path+"/../") + "' && '" + ai.Path + "' --appimage-extract" + " && xdg-open '" + filepath.Clean(ai.Path+"/../squashfs-root") + "'\"")
		} else {
			cfg.Section("Desktop Action Extract").Key("Exec").SetValue("bash -c \"cd ~ && '" + ai.Path + "' --appimage-extract" + " && xdg-open ~/squashfs-root\"")
		}
	}

	// TODO: Add "Mount" action

	// Add "Update" action
	if ai.updateinformation != "" {
		actions = append(actions, "Update")
		cfg.Section("Desktop Action Update").Key("Name").SetValue("Update")
		cfg.Section("Desktop Action Update").Key("Exec").SetValue(os.Args[0] + " update \"" + ai.Path + "\"")
	}

	// Add "Open Containing Folder" action
	if helpers.IsCommandAvailable("xdg-open") {
		actions = append(actions, "Show")
		cfg.Section("Desktop Action Show").Key("Name").SetValue("Open Containing Folder")
		cfg.Section("Desktop Action Show").Key("Exec").SetValue("xdg-open \"" + filepath.Clean(ai.Path+"/../") + "\"")
	}

	/*
	   # For testing Firejail:
	   # The simplest and most straightforward way to get the most recent version
	   # of Firejail running on a less than recent OS; don't do this at home kids
	   FILE=$(wget -q "http://dl-cdn.alpinelinux.org/alpine/edge/main/x86_64/" -O - | grep musl-1 | head -n 1 | cut -d '"' -f 2)
	   wget -c "http://dl-cdn.alpinelinux.org/alpine/edge/main/x86_64/$FILE"
	   FILE=$(wget -q "http://dl-cdn.alpinelinux.org/alpine/edge/community/x86_64/" -O - | grep firejail-0 | head -n 1 | cut -d '"' -f 2)
	   wget -c "http://dl-cdn.alpinelinux.org/alpine/edge/community/x86_64/$FILE"
	   sudo tar xf musl-*.apk -C / 2>/dev/null
	   sudo tar xf firejail-*.apk -C / 2>/dev/null
	   sudo chown root:root /usr/bin/firejail ; sudo chmod u+s /usr/bin/firejail # suid
	*/

	// Add "Run in Firejail" action
	// TODO: Based on what the AppImage author has specified, run AppImages by default
	// with the matching subsets of rights, e.g., without network access
	if helpers.IsCommandAvailable("firejail") {
		actions = append(actions, "Firejail")
		cfg.Section("Desktop Action Firejail").Key("Name").SetValue("Run in Firejail")
		cfg.Section("Desktop Action Firejail").Key("Exec").SetValue("firejail --env=DESKTOPINTEGRATION=appimaged --noprofile --appimage \"" + ai.Path + "\"")

		actions = append(actions, "FirejailNoNetwork")
		cfg.Section("Desktop Action FirejailNoNetwork").Key("Name").SetValue("Run in Firejail Without Network Access")
		cfg.Section("Desktop Action FirejailNoNetwork").Key("Exec").SetValue("firejail --env=DESKTOPINTEGRATION=appimaged --noprofile --net=none --appimage \"" + ai.Path + "\"")

		actions = append(actions, "FirejailPrivate")
		cfg.Section("Desktop Action FirejailPrivate").Key("Name").SetValue("Run in Private Firejail Sandbox")
		cfg.Section("Desktop Action FirejailPrivate").Key("Exec").SetValue("firejail --env=DESKTOPINTEGRATION=appimaged --noprofile --private --appimage \"" + ai.Path + "\"")

		actions = append(actions, "FirejailOverlayTmpfs")
		cfg.Section("Desktop Action FirejailOverlayTmpfs").Key("Name").SetValue("Run in Firejail with Temporary Overlay Filesystem")
		cfg.Section("Desktop Action FirejailOverlayTmpfs").Key("Exec").SetValue("firejail --env=DESKTOPINTEGRATION=appimaged --noprofile --overlay-tmpfs --appimage \"" + ai.Path + "\"")
	}

	as := ""
	for _, action := range actions {
		as = as + action + ";"
	}
	cfg.Section("Desktop Entry").Key("Actions").SetValue(as)

	if *verbosePtr {
		log.Println("desktop: Saving to", desktopcachedir+"/"+filename)
	}
	err = cfg.SaveTo(desktopcachedir + "/" + filename)
	if err != nil {
		log.Printf("Fail to write file: %v", err)
	}

	err = fixDesktopFile(desktopcachedir + "/" + filename)
	if err != nil {
		helpers.PrintError("desktop fixDesktopFile", err)
		os.Exit(1)
	}
}

// Return true if a path to a file is writable
func isWritable(path string) bool {
	return unix.Access(path, unix.W_OK) == nil
}

// Really ugly workaround for
// https://github.com/go-ini/ini/issues/90
func fixDesktopFile(path string) error {
	input, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	var output []byte
	if bytes.Contains(input, []byte("=`")) {
		output = bytes.Replace(input, []byte("=`"), []byte("="), -1)
		output = bytes.Replace(output, []byte("`\n"), []byte("\n"), -1)
	}
	output = bytes.ReplaceAll(output, []byte("；"), []byte(";"))

	if err = ioutil.WriteFile(path, output, 0755); err != nil {
		return err
	}
	return nil
}
