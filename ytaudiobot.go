package main

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/otium/ytdl"
)

const filesDirPath string = "./tmp/"

func ffmpegDecode(title string, extension string) (string, error) {
	mp3FileName := fmt.Sprintf("%s%s.%s", filesDirPath, title, "mp3")
	if _, err := os.Stat(mp3FileName); err == nil {
		os.Remove(mp3FileName)
	}
	videoName := fmt.Sprintf("%s%s.%s", filesDirPath, title, extension)
	fmt.Printf("Start decoding %s\n", videoName)
	cmd := exec.Command("ffmpeg", "-i", videoName, mp3FileName)
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	fmt.Printf("ffmpeg output: %q\n", out.String())
	return mp3FileName, err
}

var pendingAnswers = make(map[int64]bool)

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
		} else {
			appConfig.AuthorizedIDs = append(appConfig.AuthorizedIDs, chatID)
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
	if !isAuthorized {
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
		pendingAnswers[chatID] = true
		return
	}
	_, err := url.ParseRequestURI(message.Text)
	if err != nil {
		fmt.Printf("Not url, skipping message")
		return
	}
	vid, err := ytdl.GetVideoInfo(message.Text)
	if vid == nil || err != nil {
		fmt.Printf("Error getting video info. %s", err.Error())
		return
	}
	videoDurationInMinutes := int64(vid.Duration.Minutes())
	if videoDurationInMinutes > cfg.MaxVideoDurationMinutes {
		fmt.Printf("Max video duration exceeded, download skipped")
		msg := tgbotapi.NewMessage(chatID,
			fmt.Sprintf("I am not allowed to download videos longer than %d minutes :(", cfg.MaxVideoDurationMinutes))
		_, err = bot.Send(msg)
		if err != nil {
			fmt.Println(err.Error())
		}
		return
	}

	bestFormats := vid.Formats.Best(ytdl.FormatAudioBitrateKey)

	if len(bestFormats) == 0 || bestFormats[0].AudioBitrate == 0 {
		fmt.Printf("Video has no suitable AudioBitrate, download failed") //TODO message for tg-user?
		return
	}
	bestFormat := bestFormats[0]
	escapedVideoTitle := strings.Replace(vid.Title, "/", "", -1) // / - may be interpreted as path
	newFileName := fmt.Sprintf("%s%s.%s", filesDirPath, escapedVideoTitle, bestFormat.Extension)
	videoFile, err := os.Create(newFileName)
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	fmt.Printf("started downloading video %s\n", newFileName) //TODO hide debug info
	err = vid.Download(bestFormat, videoFile)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	videoFile.Close()
	fmt.Printf("successfully finished downloading video %s\n", newFileName)
	mp3FileName, err := ffmpegDecode(escapedVideoTitle, bestFormat.Extension)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	if _, err := os.Stat(newFileName); err == nil { //remove video file after success
		err = os.Remove(newFileName)
		if err != nil {
			fmt.Println(err.Error())
			return
		}
	}
	audioCfg := tgbotapi.NewAudioUpload(message.Chat.ID, mp3FileName)
	audioCfg.ReplyToMessageID = message.MessageID

	_, err = bot.Send(audioCfg)

	if err != nil {
		fmt.Println(err.Error())
		return
	}
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	if _, err := os.Stat(mp3FileName); err == nil { //remove mp3 after success
		err = os.Remove(mp3FileName)
		if err != nil {
			fmt.Println(err.Error())
			return
		}
	}
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
