// Package main generates Windows resource manifests.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/tc-hib/winres"
	winresversion "github.com/tc-hib/winres/version"
)

const resourceTemplate = `{
  "RT_GROUP_ICON": {
    "#2": {
      "0000": [
        "icon_256x256.png"
      ]
    }
  },
  "RT_MANIFEST": {
    "#1": {
      "0409": {
        "identity": {
          "name": "%s",
          "version": "%s"
        },
        "description": "Go Music DL desktop application",
        "minimum-os": "vista",
        "execution-level": "as invoker",
        "ui-access": false,
        "auto-elevate": false,
        "dpi-awareness": "system",
        "disable-theming": false,
        "disable-window-filtering": false,
        "high-resolution-scrolling-aware": false,
        "ultra-high-resolution-scrolling-aware": false,
        "long-path-aware": false,
        "printer-driver-isolation": false,
        "gdi-scaling": false,
        "segment-heap": false,
        "use-common-controls-v6": false
      }
    }
  },
  "RT_VERSION": {
    "#1": {
      "0000": {
        "fixed": {
          "file_version": "%s",
          "product_version": "%s",
          "timestamp": "%s"
        },
        "info": {
          "0409": {
            "Comments": "A complete, engineered Go music download project with CLI and Web interface",
            "CompanyName": "guohuiyuan",
            "FileDescription": "https://github.com/guohuiyuan/go-music-dl",
            "FileVersion": "%s",
            "InternalName": "%s",
            "LegalCopyright": "%s",
            "LegalTrademarks": "",
            "OriginalFilename": "%s",
            "PrivateBuild": "",
            "ProductName": "Go Music DL",
            "ProductVersion": "%s",
            "SpecialBuild": ""
          }
        }
      }
    }
  }
}`

const timeFormat = "2006-01-02T15:04:05+08:00"

// desktopIconPath is the multi-resolution favicon selected for the EXE.
const desktopIconPath = "../internal/web/templates/static/icon/favicon.ico"

type resourceSpec struct {
	outputFile       string
	identityName     string
	internalName     string
	originalFileName string
	sysoPath         string
}

func main() {
	fileVersion := resolveFileVersion()
	productVersion := "v1.0.0"
	now := time.Now()
	timestamp := now.Format(timeFormat)
	copyright := "(c) 2026 guohuiyuan. All Rights Reserved."

	specs := []resourceSpec{
		{
			outputFile:       "winres.json",
			identityName:     "go-music-dl-desktop",
			internalName:     "go-music-dl-desktop",
			originalFileName: "go-music-dl-desktop.exe",
		},
		{
			outputFile:       "desktop_go.winres.json",
			identityName:     "music-dl-desktop-go",
			internalName:     "music-dl-desktop-go",
			originalFileName: "music-dl-desktop-go.exe",
			sysoPath:         "../desktop_go/rsrc_windows_amd64.syso",
		},
	}

	for _, spec := range specs {
		if err := writeResourceFile(spec, fileVersion, productVersion, timestamp, copyright); err != nil {
			panic(err)
		}
	}

	if err := writeDesktopSyso(specs[len(specs)-1], fileVersion, productVersion, now); err != nil {
		panic(err)
	}
}

func resolveFileVersion() string {
	var stdout strings.Builder
	cmd := exec.Command("git", "rev-list", "--count", "HEAD")
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return "1.0.0.0"
	}

	commitCount := strings.TrimSpace(stdout.String())
	if commitCount == "" {
		return "1.0.0.0"
	}

	return "1.0.0." + commitCount
}

func writeResourceFile(
	spec resourceSpec,
	fileVersion string,
	productVersion string,
	timestamp string,
	copyright string,
) error {
	f, err := os.Create(spec.outputFile)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = fmt.Fprintf(
		f,
		resourceTemplate,
		spec.identityName,
		fileVersion,
		fileVersion,
		productVersion,
		timestamp,
		fileVersion,
		spec.internalName,
		copyright,
		spec.originalFileName,
		productVersion,
	)
	return err
}

func writeDesktopSyso(spec resourceSpec, fileVersion string, productVersion string, timestamp time.Time) error {
	if spec.sysoPath == "" {
		return nil
	}

	iconFile, err := os.Open(desktopIconPath)
	if err != nil {
		return fmt.Errorf("open %s: %w", desktopIconPath, err)
	}
	defer iconFile.Close()

	appIcon, err := winres.LoadICO(iconFile)
	if err != nil {
		return fmt.Errorf("load %s: %w", desktopIconPath, err)
	}

	var numericVersion [4]uint16
	if _, err := fmt.Sscanf(fileVersion, "%d.%d.%d.%d",
		&numericVersion[0], &numericVersion[1],
		&numericVersion[2], &numericVersion[3],
	); err != nil {
		return fmt.Errorf("parse version %s: %w", fileVersion, err)
	}

	info := winresversion.Info{
		FileVersion:    numericVersion,
		ProductVersion: [4]uint16{1, 0, 0, 0},
		Timestamp:      timestamp,
	}
	stringValues := map[string]string{
		winresversion.Comments:         "A complete, engineered Go music download project with CLI and Web interface",
		winresversion.CompanyName:      "guohuiyuan",
		winresversion.FileDescription:  "https://github.com/guohuiyuan/go-music-dl",
		winresversion.FileVersion:      fileVersion,
		winresversion.InternalName:     spec.internalName,
		winresversion.LegalCopyright:   "(c) 2026 guohuiyuan. All Rights Reserved.",
		winresversion.LegalTrademarks:  "",
		winresversion.OriginalFilename: spec.originalFileName,
		winresversion.PrivateBuild:     "",
		winresversion.ProductName:      "Go Music DL",
		winresversion.ProductVersion:   productVersion,
		winresversion.SpecialBuild:     "",
	}
	for key, value := range stringValues {
		if err := info.Set(winresversion.LangDefault, key, value); err != nil {
			return fmt.Errorf("set version info %s: %w", key, err)
		}
	}

	resources := winres.ResourceSet{}
	if err := resources.SetIcon(winres.ID(2), appIcon); err != nil {
		return fmt.Errorf("set application icon: %w", err)
	}
	resources.SetVersionInfo(info)
	resources.SetManifest(winres.AppManifest{
		Identity: winres.AssemblyIdentity{
			Name:    spec.identityName,
			Version: numericVersion,
		},
		Description:   "Go Music DL desktop application",
		Compatibility: winres.WinVistaAndAbove,
		DPIAwareness:  winres.DPIAware,
	})

	output, err := os.Create(spec.sysoPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", spec.sysoPath, err)
	}
	defer output.Close()
	if err := resources.WriteObject(output, winres.ArchAMD64); err != nil {
		return fmt.Errorf("write %s: %w", spec.sysoPath, err)
	}
	return nil
}
