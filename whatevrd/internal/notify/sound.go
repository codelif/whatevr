package notify

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// The freedesktop sound-naming-spec event for an incoming instant message. Used
// both as the libcanberra event id and to locate a fallback file in the theme.
const messageSoundName = "message-new-instant"

// Candidate theme files searched when falling back to a raw audio player. Order
// matters: the dedicated message event first, then a generic "complete" chime.
var soundFileCandidates = []string{
	"/usr/share/sounds/freedesktop/stereo/" + messageSoundName + ".oga",
	"/usr/share/sounds/freedesktop/stereo/complete.oga",
	"/usr/share/sounds/freedesktop/stereo/message.oga",
}

// soundPlayer is resolved once: how to play the notification sound on this host.
// argv is the command to run; when it ends in a placeholder the resolved theme
// file is appended. A nil argv means no usable player was found.
type soundPlayer struct {
	argv []string
}

var (
	soundOnce   sync.Once
	soundCached *soundPlayer
)

// resolveSoundPlayer picks a playback command, preferring libcanberra (which
// honours the XDG sound theme and the user's event-sound settings) and falling
// back to raw players against a theme file. Resolved lazily and cached.
func resolveSoundPlayer() *soundPlayer {
	soundOnce.Do(func() {
		// libcanberra: plays the named event straight from the sound theme.
		if path, err := exec.LookPath("canberra-gtk-play"); err == nil {
			soundCached = &soundPlayer{argv: []string{path, "-i", messageSoundName}}
			return
		}

		file := firstExistingSoundFile()
		if file == "" {
			return
		}
		for _, player := range []string{"pw-play", "paplay", "aplay", "ffplay"} {
			path, err := exec.LookPath(player)
			if err != nil {
				continue
			}
			argv := []string{path}
			if player == "ffplay" {
				argv = append(argv, "-nodisp", "-autoexit", "-loglevel", "quiet")
			}
			soundCached = &soundPlayer{argv: append(argv, file)}
			return
		}
	})
	return soundCached
}

func firstExistingSoundFile() string {
	candidates := soundFileCandidates
	if dirs := os.Getenv("XDG_DATA_DIRS"); dirs != "" {
		for _, dir := range filepath.SplitList(dirs) {
			candidates = append(candidates,
				filepath.Join(dir, "sounds", "freedesktop", "stereo", messageSoundName+".oga"))
		}
	}
	for _, file := range candidates {
		if info, err := os.Stat(file); err == nil && !info.IsDir() {
			return file
		}
	}
	return ""
}

// playSound plays the notification sound, best effort. It is decoupled from the
// notification server's advertised capabilities because many servers neither
// advertise nor honour the "sound" hint, leaving the toggle silent. Runs
// detached; a missing player is simply a no-op.
func playSound() {
	player := resolveSoundPlayer()
	if player == nil || len(player.argv) == 0 {
		return
	}
	cmd := exec.Command(player.argv[0], player.argv[1:]...)
	if err := cmd.Start(); err != nil {
		return
	}
	// Reap the process so it doesn't linger as a zombie.
	go func() { _ = cmd.Wait() }()
}
