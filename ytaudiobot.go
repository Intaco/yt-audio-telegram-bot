package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	re "regexp"
	"strconv"
	"strings"

	"github.com/go-telegram-bot-api/telegram-bot-api"
	ac "github.com/jfk9w-go/aconvert-api"
	"github.com/jfk9w-go/flu"
	"github.com/otium/ytdl"
)

const filesDirPath string = "./tmp/"

var pendingAnswers = make(map[int64]bool)
var aconvertAPI ac.Api

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
	outputText := strings.TrimSpace(out.String()) // normally, returns empty string if everything OK
	if len(outputText) != 0 {
		return "", errors.New("FFMPEG returned non-empty string: " + outputText)
	}
	return mp3FileName, err
}

// Decode video via AConvert, used as fallback if ffmpeg decode fails or telegram can't accept ffmpeg-decoded audio (infrequent)
func aconvertDecode(videoFileNameWithExt string, title string) (string, error) {
	mp3FileName := makeFileName(title, "mp3")
	deleteFile(mp3FileName) //remove target file if exists
	if aconvertAPI == nil { //init aconvertAPI once
		aconvertAPI = ac.NewApi(nil, ac.Config{
			TestFile:   videoFileNameWithExt,
			TestFormat: "mp3",
		})
	}

	fmt.Printf("Start AConvert decoding %s\n", videoFileNameWithExt)

	r, err := aconvertAPI.Convert(
		flu.NewFileSystemResource(videoFileNameWithExt), ac.NewOpts().TargetFormat("mp3"))
	if err != nil {
		return "", err
	}
	return mp3FileName, aconvertAPI.Download(r, flu.NewFileSystemResource(mp3FileName))
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

	var regRule = re.MustCompile("[|/]*")
	escapedVideoTitle := regRule.ReplaceAllString(vid.Title, "") // / - may be interpreted as path
	videoFileNameWithExt := makeFileName(escapedVideoTitle, bestFormat.Extension)
	videoFile, err := os.Create(videoFileNameWithExt)
	if err != nil {
		fmt.Println(err.Error())
		return
	}

	fmt.Printf("started downloading video %s\n", videoFileNameWithExt) //TODO hide debug info
	err = vid.Download(bestFormat, videoFile)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	videoFile.Close()
	fmt.Printf("successfully finished downloading video %s\n", videoFileNameWithExt)
	mp3FileName, err := ffmpegDecode(videoFileNameWithExt, escapedVideoTitle)
	if err != nil {
		videoWasFFMPEGDecoded = false
		fmt.Printf("Failed decoding %s with FFMPEG, trying AConvert...\n%s", videoFileNameWithExt, err.Error())
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
