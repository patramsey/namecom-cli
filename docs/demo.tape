# Demo recording script for the namecom CLI. To record it: install vhs
# (https://github.com/charmbracelet/vhs) plus its ffmpeg/ttyd runtime deps,
# then from the repository root:
#
#   make build && vhs docs/demo.tape
#
# `make build` matters. The recorded shell puts the repository first on PATH
# so `namecom` resolves to ./namecom, the binary just built from this working
# tree. Without that it resolves to whatever `namecom` is installed system-wide
# — on a machine with a Homebrew install, that is a released build, and the
# demo would show an older version's output while looking current. Note
# `make install` does not help: `go install` names the binary after the module
# path, so it lands as `namecom-cli`, not `namecom`.
#
# THIS RECORDS AGAINST THE SANDBOX (api.dev.name.com), never production.
# Registering a domain for real spends real money. The tape sets no --profile
# and no NAMECOM_PROFILE: it records whatever the ACTIVE profile is, so before
# recording, confirm that profile is a sandbox one:
#
#   namecom auth status     # Environment must read "sandbox"
#
# That check is deliberately not a step in the tape, because `auth status`
# prints the account username, which does not belong in a GIF on a README.
# Set sandbox credentials up once with:
#
#   namecom auth login --profile sandbox --sandbox
#
# Log in BEFORE recording, never during — the token prompt must not land in a
# frame.
#
# RE-RECORDING: the registration is real within the sandbox account, so once
# this succeeds the domain below is taken and the next run stops at "not
# available". Pick a fresh name, verify it first with `namecom domain check`,
# and update every occurrence in this file. There is no way to re-use a name:
# the tape depends on the domain being available, so every re-record costs one.
#
# KEEP THE NAME TO 16 CHARACTERS OR FEWER. At this width and font size the
# terminal is about 75 columns, and the longest line the demo prints is
#
#   → Run 'namecom domain autorenew on <domain>' to enable auto-renewal
#
# which is 59 columns plus the domain. `driftwoodco.com` (15) ended flush with
# the right edge; a 17-character name wrapped and left a single "l" stranded on
# its own line. Check the recording, not just the tape — nothing here fails
# when the name is too long, it just looks broken.
#
# Keep this in sync: re-run it whenever a change touches what these commands
# print (flags, output formatting, hints, prompts, sample values).

Output docs/demo.gif

Require namecom

Env NAMECOM_ICONS "1"

Set Shell "bash"
Set FontSize 20
Set Width 1200
Set Height 640
Set Theme "Dracula"
Set TypingSpeed 40ms
Set Padding 20

# Hidden setup. Neither of these belongs on screen:
#   - a bare prompt, because the default bash prompt puts the recording
#     machine's hostname and working directory in every frame
#   - the repository first on PATH, so `namecom` is ./namecom from this tree
Hide
Type `export PS1="$ "`
Enter
Type `export PATH="$PWD:$PATH"`
Enter
Type "clear"
Enter
Show

Sleep 600ms

# The whole demo is one command. `domain check` prints availability and price,
# then — in an interactive terminal — offers to register it inline, so there is
# no second command to type.
#
# SLEEP BUDGET. The two sleeps that follow a keystroke-consuming prompt are
# sized against measured sandbox latency, not guessed: a keystroke sent while a
# prompt is still waiting gets eaten by the prompt instead of reaching the
# shell, which is exactly how an earlier take ended up running "amecom".
# Measured: domain check 0.46s, inline registration 2.86s, dns create 0.31s.
# The rest is reading time. Re-measure before shortening these further.
Type "namecom domain check lanterncove.com"
Sleep 300ms
Enter

# Availability table, then the "Register …?" confirm. 1.5s covers a 0.46s call.
Sleep 1500ms

# Answer the confirm. huh renders Yes/No with No selected; "y" picks Yes.
Type "y"
Sleep 400ms
Enter

# Registration plus its hints. 4.5s over a measured 2.86s — the tightest
# margin in the tape, and the one that breaks the recording if it is wrong.
Sleep 4500ms

# Point the new domain somewhere. Nothing is typed after this, so the trailing
# sleep is purely for reading and can be trimmed freely.
Type "namecom dns create lanterncove.com --type A --answer 1.2.3.4"
Sleep 300ms
Enter
Sleep 2s

Sleep 1s
