# tiktok-favvideo-downloader

Backup your favorite'd tiktok videos in case this upcoming ban means you don't get access to them anymore.

While it grabs favorite'd videos by default, it will grab your liked videos too if you say yes at the prompt.

Binary is not signed (didn't have time+budget), you WILL have to tell Windows you trust it. 

![Release](https://github.com/ozskywalker/tiktok-favvideo-downloader/actions/workflows/release-on-push-to-release-branch.yml/badge.svg)

# Let's go... how do I make this work?!

## First, get your list of favorite videos from Tiktok

1. Go to https://www.tiktok.com/setting
2. Under Privacy, Data, click on "Download your data"
3. [Select "All Data" & "JSON", then hit Request Data](https://github.com/ozskywalker/tiktok-favvideo-downloader/blob/main/readme_images/tiktok_download_data_options.png)
4. [Go to Download data tab, wait for "Download" button to appear (can take ~2-5min)](https://github.com/ozskywalker/tiktok-favvideo-downloader/blob/main/readme_images/tiktok_ready_to_download.png)
5. Download and extract the archive into a folder

![screenshot1](https://github.com/ozskywalker/tiktok-favvideo-downloader/blob/main/readme_images/tiktok_download_data_options.png)
![screenshot2](https://github.com/ozskywalker/tiktok-favvideo-downloader/blob/main/readme_images/tiktok_ready_to_download.png)

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

* **Compatible with Windows x86-64 only** - if you want an Windows ARM64 release, [leave an emoji or add a comment on this ARM64 feature request](https://github.com/ozskywalker/tiktok-favvideo-downloader/issues/1) and I'll respond accordingly.

* Uses [yt-dlp](https://github.com/yt-dlp/yt-dlp) to download the videos, play the resulting files with [VLC](https://www.videolan.org/vlc/) or your favorite media player. This isn't rocket science.