package main

import (
	"encoding/json"
	"io/ioutil"
	"os"
)

type appConfig struct {
	BotAPIKey     string  `json:"BotAPIKey"`
	AdminID       int64   `json:"AdminID"`
	AuthorizedIDs []int64 `json:"AuthorizedIDs"`
	BannedIDs     []int64 `json:"BannedIDs"`
}

func (cfg *appConfig) write() error {
	data, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return err
	}
	f, err := ioutil.TempFile(".", "config")
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if err != nil {
		return err
	}
	f.Close()
	if _, err := os.Stat("config.json"); err == nil {
		os.Remove("config.json")
	}
	if err != nil {
		return err
	}
	err = os.Rename(f.Name(), "config.json")
	return err
}
func (cfg *appConfig) load() error {
	configFile, err := os.Open("config.json")
	defer configFile.Close()
	if err != nil {
		return err
	}
	jsonParser := json.NewDecoder(configFile)
	err = jsonParser.Decode(&cfg)
	return err
}
