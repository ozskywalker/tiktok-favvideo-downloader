# tiktok-favvideo-downloader

Backup all those tiktok videos you've favorited & liked, just like the digital hoarder you really truly are.

Binary is not signed (didn't have time+budget), you WILL have to tell Windows you trust it.

![Release](https://github.com/ozskywalker/tiktok-favvideo-downloader/actions/workflows/release-on-tag.yml/badge.svg)
[![Go Version](https://img.shields.io/badge/Go-1.25.1-blue.svg)](https://golang.org/doc/devel/release.html)
![Claude Used](https://img.shields.io/badge/Claude-Used-4B5AEA)

# Let's go... how do I make this work?!

## First, get your list of favorite videos from Tiktok

1. Go to https://www.tiktok.com/setting
2. Under Privacy, Data, click on "Download your data"
3. [Select "JSON" under file format, then "Custom" and "Likes and Favorites" under data to download, then hit Request Data](https://github.com/ozskywalker/tiktok-favvideo-downloader/blob/main/readme_images/tiktok_download_data_options.png)
4. [Go to Download data tab, wait for "Download" button to appear (can take ~2-5min)](https://github.com/ozskywalker/tiktok-favvideo-downloader/blob/main/readme_images/tiktok_ready_to_download.png)
5. Download and extract the archive into a folder

![screenshot1](https://github.com/ozskywalker/tiktok-favvideo-downloader/blob/main/readme_images/tiktok_download_data_options.png)
![screenshot2](https://github.com/ozskywalker/tiktok-favvideo-downloader/blob/main/readme_images/tiktok_ready_to_download.png)

### If you get a "Maximum attempts" error when downloading...

This may be required if you have MFA, and you find Tiktok complains of a maximum attempts error.

1. Open Tiktok on Mobile
2. Under Profile, Settings and Privacy, Account, tap on Download your data
3. Go to the Download data tab, and hit the Download button from the request you created above.
4. Upload the file to your desktop (try upload via google drive, onedrive, etc.)


## Second, feed your list into the utility
1. [Download the .exe from the Releases page](https://github.com/ozskywalker/tiktok-favvideo-downloader/releases)
2. Place the .exe in the **same extracted folder** as the JSON file
3. Double-click on tiktok-favvideo-downloader.exe
4. Windows will complain because the .exe isn't signed; **simply click "More Info" then "Run anyway" one-time.**
5. Let it run!

Your video files will appear in the same folder as the .exe.

## But wait! I downloaded the files, but there's a blank screen when I try to play them!

Your Windows PC or video player is missing a codec that Microsoft does not ship by default (ask the lawyers why).

Either:
1. Use [VLC](https://www.videolan.org/vlc/) to play the videos
2. Or, [go buy this from the Microsoft Store (99 cents)](https://apps.microsoft.com/detail/9nmzlz57r3t7?hl=en-us&gl=US).

## Everything else

* **Found a problem?** [Open an issue and tell me all about it.](https://github.com/ozskywalker/tiktok-favvideo-downloader/issues)

* **Power users:** You can also drag & drop a .txt file full of links onto the .exe, or run it from a command prompt/Windows Terminal window

* Uses [yt-dlp](https://github.com/yt-dlp/yt-dlp) to download the videos, play the resulting files with [VLC](https://www.videolan.org/vlc/) or your favorite media player. This isn't rocket science.