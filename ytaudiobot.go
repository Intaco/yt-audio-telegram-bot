package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/go-telegram-bot-api/telegram-bot-api"
	ac "github.com/jfk9w-go/aconvert-api"
	"github.com/jfk9w-go/flu"
)

const filesDirPath string = "./tmp/"

var pendingAnswers = make(map[int64]bool)
var aconvertAPI *ac.Client

func makeFileName(title string, extension string) string {
	return fmt.Sprintf("%s%s.%s", filesDirPath, title, extension)
}

func deleteFile(fileName string) {
	if _, err := os.Stat(fileName); err == nil { //remove mp3 after success
		err = os.Remove(fileName)
		if err != nil {
			fmt.Println(err.Error())
			return
		}
	}
}

//Decode by FFMPEG by default
func ffmpegDecode(videoFileNameWithExt string, title string) (string, error) {
	mp3FileName := makeFileName(title, "mp3")
	deleteFile(mp3FileName) //remove target file if exists
	fmt.Printf("Start FFMPEG decoding %s\n", videoFileNameWithExt)
	cmd := exec.Command("ffmpeg", "-i", videoFileNameWithExt, mp3FileName)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return "", err
	}
	outputText := strings.TrimSpace(out.String()) // normally, returns empty string if everything OK
	if len(outputText) != 0 {
		return "", errors.New("FFMPEG returned non-empty string: " + outputText)
	}
	fmt.Printf("FFMPEG decoded normally: %s\n", videoFileNameWithExt)
	return mp3FileName, err
}

// Decode video via AConvert, used as fallback if ffmpeg decode fails or telegram can't accept ffmpeg-decoded audio (infrequent)
func aconvertDecode(videoFileNameWithExt string, title string) (string, error) {
	mp3FileName := makeFileName(title, "mp3")
	deleteFile(mp3FileName) //remove target file if exists
	fluMp3File := flu.File(mp3FileName)
	if aconvertAPI == nil { //init aconvertAPI once
		aconvertAPI = ac.NewClient(nil, nil, nil)
	}

	fmt.Printf("Start AConvert decoding %s\n", videoFileNameWithExt)
	r, err := aconvertAPI.Convert(context.Background(), flu.File(videoFileNameWithExt), make(ac.Opts).TargetFormat("mp3"))
	if err != nil {
		return "", err
	}
	err = aconvertAPI.GET(r.URL()).Execute().DecodeBodyTo(fluMp3File).Error
	return mp3FileName, err
}

func handleCallbackQuery(bot *tgbotapi.BotAPI, query *tgbotapi.CallbackQuery, appConfig AppConfig) (AppConfig, error) {
	parts := strings.Split(query.Data, ".")
	if len(parts) != 2 {
		return appConfig, fmt.Errorf("InlineKeyboardButton data incorrect, skipping")
	}
	status := parts[0]
	chatID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return appConfig, err
	}
	answerText := "Successfully added chat to whitelist!"
	_, ok := pendingAnswers[chatID]
	if !ok {
		answerText = "Command not actual! Skipping..."
	} else {
		if status != "OK" {
			answerText = "Chat was blacklisted for bot!"
			appConfig.BannedIDs = append(appConfig.BannedIDs, chatID)
			bot.Send(tgbotapi.NewMessage(chatID, "You were not allowed to use that bot. Sorry.")) //ignore result
		} else {
			appConfig.AuthorizedIDs = append(appConfig.AuthorizedIDs, chatID)
			bot.Send(tgbotapi.NewMessage(chatID, "Successfully authenticated. Now you can download videos!")) //ignore result
		}
		delete(pendingAnswers, chatID)
	}
	cbConfig := tgbotapi.NewCallback(query.ID, answerText)
	_, err = bot.AnswerCallbackQuery(cbConfig)
	return appConfig, err
}
func handleMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message, cfg AppConfig) {
	chatID := message.Chat.ID
	isBanned := false
	videoWasFFMPEGDecoded := true

	for _, id := range cfg.BannedIDs {
		if id == chatID {
			isBanned = true
			break
		}
	}
	if isBanned {
		fmt.Printf("Message from banned chat %d skipped", chatID)
		return
	}
	isAuthorized := cfg.AdminID == 0 //allow from anywhere except banned if admin not defined
	if !isAuthorized {
		for _, id := range cfg.AuthorizedIDs {
			if id == chatID {
				isAuthorized = true
				break
			}
		}
	}
	_, isAlreadyPending := pendingAnswers[chatID]
	if !isAuthorized && !isAlreadyPending {
		targetName := message.Chat.Title
		if targetName == "" {
			targetName = fmt.Sprintf("user *@%s*", message.From.UserName)
		} else {
			targetName = fmt.Sprintf("chat *%s*", targetName)
		}
		okData := fmt.Sprintf("OK.%d", chatID)
		cancelData := fmt.Sprintf("CANCEL.%d", chatID)
		btns := []tgbotapi.InlineKeyboardButton{
			{Text: "OK", CallbackData: &okData},
			{Text: "Cancel", CallbackData: &cancelData}}

		infoString := fmt.Sprintf("Message from unregistered: %s\nAllow video decoding?", targetName)
		questionMsg := tgbotapi.NewMessage(cfg.AdminID, infoString)
		questionMsg.ParseMode = "Markdown"
		questionMsg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(btns)

		forwardMsg := tgbotapi.NewForward(cfg.AdminID, chatID, message.MessageID)
		_, err := bot.Send(forwardMsg)
		_, err = bot.Send(questionMsg)

		if err != nil {
			fmt.Println(err.Error())
			return
		}
		bot.Send(tgbotapi.NewMessage(chatID, "Awaiting authentication from bot admin...")) //ignore if fails
		pendingAnswers[chatID] = true
		return
	}
	_, err := url.ParseRequestURI(message.Text)
	if err != nil {
		fmt.Printf("Not url, skipping message")
		return
	}

	isVideoDurationOk, err := isVideoInDurationLimits(message.Text, cfg.MaxVideoDurationMinutes)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	if !isVideoDurationOk {
		fmt.Printf("Max video duration exceeded, download skipped")
		msg := tgbotapi.NewMessage(chatID,
			fmt.Sprintf("I am not allowed to download videos longer than %d minutes :(", cfg.MaxVideoDurationMinutes))
		_, err = bot.Send(msg)
		if err != nil {
			fmt.Println(err.Error())
		}
		return
	}
	err, videoFileNameWithExt, escapedVideoTitle, _ := downloadVideo(message.Text, filesDirPath)
	if err != nil {
		fmt.Println("video download failed: ", err.Error())
		return
	}

	mp3FileName, err := ffmpegDecode(videoFileNameWithExt, escapedVideoTitle)
	if err != nil {
		videoWasFFMPEGDecoded = false
		fmt.Printf("Failed decoding %s with FFMPEG: %s, now trying AConvert...\n", videoFileNameWithExt, err.Error())
		mp3FileName, err = aconvertDecode(videoFileNameWithExt, escapedVideoTitle)
		if err != nil {
			fmt.Printf("Failed decoding %s with AConvert, returning...\n%s", videoFileNameWithExt, err.Error())
			deleteFile(videoFileNameWithExt) //remove video file after convert fail
			return
		}
	}

	audioCfg := tgbotapi.NewAudioUpload(message.Chat.ID, mp3FileName) //TODO mp3FileName
	audioCfg.ReplyToMessageID = message.MessageID

	_, err = bot.Send(audioCfg)

	if err != nil {
		fmt.Println(err.Error())
		if videoWasFFMPEGDecoded { //if failed but has not tried AConvert decode, try AConvert
			videoWasFFMPEGDecoded = false
			mp3FileName, err = aconvertDecode(videoFileNameWithExt, escapedVideoTitle)
			if err != nil {
				fmt.Println(err.Error())
			} else {
				audioCfg := tgbotapi.NewAudioUpload(message.Chat.ID, mp3FileName) //TODO mp3FileName
				audioCfg.ReplyToMessageID = message.MessageID
				_, err = bot.Send(audioCfg)
				if err != nil {
					fmt.Println(err.Error())
				}
			}

		}
	}
	deleteFile(mp3FileName)          //remove mp3 after all
	deleteFile(videoFileNameWithExt) //remove video file after all
	return
}
func main() {
	os.RemoveAll(filesDirPath)
	err := os.MkdirAll(filesDirPath, os.ModePerm)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	cfg, err := loadConfig()
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	bot, err := tgbotapi.NewBotAPI(cfg.BotAPIKey)

	if err != nil {
		fmt.Println(err.Error()) //TODO not verbose
		return
	}
	bot.Debug = false
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates, err := bot.GetUpdatesChan(u)
	for update := range updates {
		if update.Message == nil {
			if update.CallbackQuery != nil {
				newCfg, err := handleCallbackQuery(bot, update.CallbackQuery, cfg)
				if err != nil {
					fmt.Println(err.Error())
				} else {
					err = writeConfig(newCfg)
					if err != nil {
						fmt.Println(err.Error())
					} else {
						cfg = newCfg
					}
				}
			} else if update.ChannelPost != nil {
				go handleMessage(bot, update.ChannelPost, cfg)
			}
			continue
		} else {
			go handleMessage(bot, update.Message, cfg)
		}
	}
}
