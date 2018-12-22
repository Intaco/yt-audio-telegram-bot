# yt-audio-telegram-bot

A configurable telegram bot for downloading, converting and publishing youtube videos with authorization support.

# Prerequisites

This application expects that you have a `ffmpeg` binary in your environment. For Ubuntu you can just write
```
$ sudo apt install ffmpeg
```
You also have to create a `config.json` in your executable folder. Here is the configuration sample:
```
{
    "BotAPIKey": "YOUR_BOT_TOKEN_HERE",
    "AdminID": 0, 
    "AuthorizedIDs": [],
    "BannedIDs": [],
    "MaxVideoDurationMinutes": 20
}
```
`AdminID` - admin user's telegram ID. This user will have permissions to allow bot usage for the others. If left 0, all users will be permitted.
`AuthorizedIDs` and `BannedIDs` - list of comma-separated authorized/banned telegram UID's. May be left as empty array. These two fields may get updated while running the app.
`MaxVideoDurationMinutes` - surprisingly, the length limit for downloadable videos. The recommended value is 20 minutes.