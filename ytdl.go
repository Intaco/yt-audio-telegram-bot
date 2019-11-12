package main

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	re "regexp"
	"strconv"
	"strings"
)

func isVideoInDurationLimits(uri string, maxDurationMinutes int64) (bool, error) {
	cmd := exec.Command("youtube-dl", "--get-duration", uri)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return false, err
	}
	outputText := strings.TrimSpace(out.String()) // normally, returns duration string if OK
	outputTextSplit := strings.Split(outputText, ":")
	possibleError := errors.New("youtube-dl returned unparsable video duration string: " + outputText)
	durationMins, err := strconv.Atoi(outputTextSplit[0])
	if err != nil {
		return false, possibleError
	} else {
		return int64(durationMins) < maxDurationMinutes, nil
	}
}

func getTitleAndExt(path string) (error, string, string) {
	var ext = filepath.Ext(path)
	var name = path[0 : len(path)-len(ext)]
	if ext == "" {
		return errors.New("Invalid path:" + path), "", ""
	} else {
		return nil, name, ext
	}
}

func downloadVideo(uri string, filesDirectory string) (error, string, string, string) {
	var regRule = re.MustCompile("[|/\n]*") // / - may be interpreted as path

	cmd := exec.Command("youtube-dl", "-f bestaudio", "--get-filename", "-o", "%(title)s.%(ext)s", uri)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return err, "", "", ""
	}
	downloadFilename := regRule.ReplaceAllString(out.String(), "")
	fmt.Printf("started downloading video %s\n", uri)
	cmd = exec.Command("youtube-dl", "--quiet", "-f bestaudio", "-o", filesDirectory+"%(title)s.%(ext)s", uri)
	cmd.Stdout = &out
	err = cmd.Run()
	if err != nil {
		return err, "", "", ""
	}
	fmt.Printf("successfully finished downloading video %s\n", uri)
	escapedFilename := regRule.ReplaceAllString(downloadFilename, "")
	err, title, ext := getTitleAndExt(escapedFilename)
	if err != nil {
		return err, "", "", ""
	}
	return nil, filesDirectory + escapedFilename, title, ext
}
