package main

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"io/ioutil"
	"net/url"

	"gopkg.in/ini.v1"

	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/adrg/xdg"
	"github.com/sagesound/go-appimage/internal/helpers"
	"github.com/sagesound/go-appimage/src/goappimage"
	"go.lsp.dev/uri"
)

// AppImage Handles AppImage files.
type AppImage struct {
	*goappimage.AppImage
	uri               string
	md5               string
	desktopfilename   string
	desktopfilepath   string
	thumbnailfilename string
	thumbnailfilepath string
	updateinformation string
	startup           bool //used so we don't notify applications being added on startup
	// offset            int64
	// rawcontents       string
	// niceName          string
}

// NewAppImage creates an AppImage object from the location defined by path.
// The AppImage object will also be created if path does not exist,
// because the AppImage that used to be there may need to be removed
// and for this the functions of an AppImage are needed.
// Non-existing and invalid AppImages will have type -1.
func NewAppImage(path string) (ai *AppImage, err error) {
	ai = new(AppImage)
	ai.AppImage, err = goappimage.NewAppImage(path)
	ai.uri = strings.TrimSpace(string(uri.File(filepath.Clean(ai.Path))))
	ai.md5 = ai.calculateMD5filenamepart() // Need this also for non-existing AppImages for removal
	ai.desktopfilename = "appimagekit_" + ai.md5 + ".desktop"
	ai.desktopfilepath = xdg.DataHome + "/applications/" + "appimagekit_" + ai.md5 + ".desktop"
	ai.thumbnailfilename = ai.md5 + ".png"
	if strings.HasSuffix(ThumbnailsDirNormal, "/") {
		ai.thumbnailfilepath = ThumbnailsDirNormal + ai.thumbnailfilename
	} else {
		ai.thumbnailfilepath = ThumbnailsDirNormal + "/" + ai.thumbnailfilename
	}
	if err != nil {
		return ai, err
	}
	ui, err := ai.ReadUpdateInformation()
	if err == nil && ui != "" {
		ai.updateinformation = ui
	}
	return ai, nil
}

func (ai AppImage) calculateMD5filenamepart() string {
	hasher := md5.New()
	hasher.Write([]byte(ai.uri))
	return hex.EncodeToString(hasher.Sum(nil))
}

func (ai AppImage) setExecBit() {
	err := os.Chmod(ai.Path, 0755)
	if err == nil {
		if *verbosePtr {
			log.Println("appimage: Set executable bit on", ai.Path)
		}
	}
	// printError("appimage", err) // Do not print error since AppImages on read-only media are common
}

// Validate checks the quality of an AppImage and sends desktop notification, returns error or nil
// TODO: Add more checks and reuse this in appimagetool
func (ai AppImage) Validate() error {
	if *verbosePtr {
		log.Println("Validating AppImage", ai.Path)
	}
	// Check validity of the updateinformation in this AppImage, if it contains some
	if ai.updateinformation != "" {
		log.Println("Validating updateinformation in", ai.Path)
		err := helpers.ValidateUpdateInformation(ai.updateinformation)
		if err != nil {
			helpers.PrintError("appimage: updateinformation verification", err)
			return err
		}
	}
	return nil
}

// Do not call this directly. Instead, call IntegrateOrUnintegrate
// Integrate an AppImage into the system (put in menu, extract thumbnail)
// Can take a long time, hence run with "go"
func (ai AppImage) _integrate() {

	// log.Println("integrate called on:", ai.path)

	// Return immediately if the filename extension is not .AppImage or .app
	if !strings.HasSuffix(strings.ToUpper(ai.Path), ".APPIMAGE") && !strings.HasSuffix(strings.ToUpper(ai.Path), ".APP") {
		// log.Println("No .AppImage suffix:", ai.path)
		return
	}

	ai.setExecBit()

	// For performance reasons, we stop working immediately
	// in case a desktop file already exists at that location
	if !*overwritePtr {
		// Compare mtime of desktop file and AppImage, similar to
		// https://specifications.freedesktop.org/thumbnail-spec/thumbnail-spec-latest.html#MODIFICATIONS
		if desktopFileInfo, err := os.Stat(ai.desktopfilepath); err == nil {
			if appImageInfo, err := os.Stat(ai.Path); err == nil {
				diff := desktopFileInfo.ModTime().Sub(appImageInfo.ModTime())
				if diff > (time.Duration(0) * time.Second) {
					// Do nothing if the desktop file is already newer than the AppImage file
					// but subscribe
					if CheckIfConnectedToNetwork() {
						go SubscribeMQTT(MQTTclient, ai.updateinformation)
					}
					return
				}
			}
		}
	}

	// Let's be evil and integrate only good AppImages...
	// err := ai.Validate()
	// if err != nil {
	// 	log.Println("AppImage did not pass validation:", ai.path)
	// 	return
	// }

	writeDesktopFile(ai) // Do not run with "go" as it would interfere with extractDirIconAsThumbnail

	// Subscribe to MQTT messages for this application
	if ai.updateinformation != "" {
		if CheckIfConnectedToNetwork() {
			go SubscribeMQTT(MQTTclient, ai.updateinformation)
		}
	}

	// SimpleNotify(ai.path, "Integrated", 3000)

	// For performance reasons, we stop working immediately
	// in case a thumbnail file already exists at that location
	// if *overwritePtr == false {
	// Compare mtime of thumbnail file and AppImage, similar to
	// https://specifications.freedesktop.org/thumbnail-spec/thumbnail-spec-latest.html#MODIFICATIONS
	if thumbnailFileInfo, err := os.Stat(ai.thumbnailfilepath); err == nil {
		if appImageInfo, err := os.Stat(ai.Path); err == nil {
			diff := thumbnailFileInfo.ModTime().Sub(appImageInfo.ModTime())
			if diff > (time.Duration(0) * time.Second) {
				// Do nothing if the thumbnail file is already newer than the AppImage file
				return
			}
		}
	}
	// }

	ai.extractDirIconAsThumbnail() // Do not run with "go" as it would interfere with writeDesktopFile

}

// Do not call this directly. Instead, call IntegrateOrUnintegrate
func (ai AppImage) _removeIntegration() {
	log.Println("appimage: Remove integration", ai.Path)
	err := os.Remove(ai.thumbnailfilepath)
	if err == nil {
		log.Println("appimage: Deleted", ai.thumbnailfilepath)
	} else {
		log.Println("appimage:", err, ai.thumbnailfilepath)
	}

	// Unsubscribe to MQTT messages for this application
	if ai.updateinformation != "" {
		go UnSubscribeMQTT(MQTTclient, ai.updateinformation)
	}

	err = os.Remove(ai.desktopfilepath)
	if err == nil {
		log.Println("appimage: Deleted", ai.desktopfilepath)
		sendDesktopNotification("Removed", ai.Path, 3000)
	} else {
		log.Println("appimage:", err, ai.desktopfilename)
	}
}

// IntegrateOrUnintegrate integrates or unintegrates
// (registers or unregisters) an AppImage from the system,
// depending on whether the file exists on disk. NEVER call this directly,
// ONLY have this called from a function that limits parallelism and ensures
// uniqueness of the AppImages to be processed
func (ai AppImage) IntegrateOrUnintegrate() bool {
	if _, err := os.Stat(ai.Path); os.IsNotExist(err) {
		ai._removeIntegration()
	} else {
		ai._integrate()
		return true
	}
	return false
}

// ReadUpdateInformation reads updateinformation from an AppImage
// Returns updateinformation string and error
func (ai AppImage) ReadUpdateInformation() (string, error) {
	aibytes, err := helpers.GetSectionData(ai.Path, ".upd_info")
	ui := strings.TrimSpace(string(bytes.Trim(aibytes, "\x00")))
	if err != nil {
		return "", err
	}
	// Don't validate here, we don't want to get warnings all the time.
	// We have AppImage.Validate as its own function which we call less frequently than this.
	return ui, nil
}

// LaunchMostRecentAppImage launches an the most recent application for a given
// updateinformation that we found among the integrated AppImages.
// Kinda like poor man's Launch Services. Probably we should make as much use of it as possible.
// Downside: Applications without updateinformation cannot be used in this way.
func LaunchMostRecentAppImage(updateinformation string, args []string) {
	if updateinformation == "" {
		return
	}
	if !*quietPtr {
		aipath := FindMostRecentAppImageWithMatchingUpdateInformation(updateinformation)
		log.Println("Launching", aipath, args)
		cmd := []string{aipath}
		cmd = append(cmd, args...)
		err := helpers.RunCmdTransparently(cmd)
		if err != nil {
			helpers.PrintError("LaunchMostRecentAppImage", err)
		}

	}
}

// FindMostRecentAppImageWithMatchingUpdateInformation finds the most recent registered AppImage
// that havs matching upate information embedded
func FindMostRecentAppImageWithMatchingUpdateInformation(updateinformation string) string {
	results := FindAppImagesWithMatchingUpdateInformation(updateinformation)
	mostRecent := helpers.FindMostRecentFile(results)
	return mostRecent
}

// FindAppImagesWithMatchingUpdateInformation finds registered AppImages
// that have matching upate information embedded
func FindAppImagesWithMatchingUpdateInformation(updateinformation string) []string {
	files, err := ioutil.ReadDir(xdg.DataHome + "/applications/")
	helpers.LogError("desktop", err)
	var results []string
	if err != nil {
		return results
	}
	for _, file := range files {
		if strings.HasSuffix(file.Name(), ".desktop") && strings.HasPrefix(file.Name(), "appimagekit_") {

			cfg, e := ini.LoadSources(ini.LoadOptions{IgnoreInlineComment: true}, // Do not cripple lines hat contain ";"
				xdg.DataHome+"/applications/"+file.Name())
			helpers.LogError("desktop", e)
			dst := cfg.Section("Desktop Entry").Key(ExecLocationKey).String()
			_, err = os.Stat(dst)
			if os.IsNotExist(err) {
				log.Println(dst, "does not exist, it is mentioned in", xdg.DataHome+"/applications/"+file.Name())
				continue
			}
			ai, err := NewAppImage(dst)
			if err != nil {
				continue
			}
			ui, err := ai.ReadUpdateInformation()
			if err == nil && ui != "" {
				//log.Println("updateinformation:", ui)
				// log.Println("updateinformation:", url.QueryEscape(ui))
				unescapedui, _ := url.QueryUnescape(ui)
				// log.Println("updateinformation:", unescapedui)
				if updateinformation == unescapedui {
					results = append(results, ai.Path)
				}
			}

			continue
		}
	}
	return results
}
