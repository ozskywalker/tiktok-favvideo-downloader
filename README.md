# tiktok-favvideo-downloader

Backup your favorite'd tiktok videos in case this upcoming ban means you don't get access to them anymore. 

![Release](https://github.com/ozskywalker/tiktok-favvideo-downloader/actions/workflows/release-on-push-to-release-branch.yml/badge.svg?
branch=release)

# Let's go... how do I make this work?!

## First, get your list of favorite videos from Tiktok

1. Go to https://www.tiktok.com/setting
2. Under Privacy, Data, click on "Download your data"
3. Select "All Data" & "JSON", then hit Request Data
4. Wait for data to be generated, can take 5-15min
5. Download and extract the archive

## Second, feed your list into the utility
1. [Download me from the Releases page](https://github.com/ozskywalker/tiktok-favvideo-downloader/releases) and place the .exe in the same directory as the JSON file
2. Double-click on tiktok-favvideo-downloader.exe and let it run!

[ You can also drag & drop a .txt file full of links onto the .exe, or run it from cmd.exe/Windows Terminal/PowerShell - whatever your heart desires ]


## Everything else

* Found a problem? [Open an issue and tell me all about it.](https://github.com/ozskywalker/tiktok-favvideo-downloader/issues)

* Uses [yt-dlp](https://github.com/yt-dlp/yt-dlp) to download the videos, play the resulting files with [VLC](https://www.videolan.org/vlc/) or your favorite media player.

* **Compatible with Windows x86-64 only** - if you want an Windows ARM64 release, [leave an emoji or add a comment on this ARM64 feature request](https://github.com/ozskywalker/tiktok-favvideo-downloader/issues/1) and I'll respond accordingly.
