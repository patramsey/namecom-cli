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
# this succeeds DEMO.COM below is taken and the next run stops at "not
# available". Pick a fresh name (check it first with `namecom domain check`)
# or release the old one from the sandbox account.
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
Set TypingSpeed 60ms
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

Sleep 1s

# The whole demo is one command. `domain check` prints availability and price,
# then — in an interactive terminal — offers to register it inline, so there is
# no second command to type. That offer is why the sleeps below are generous:
# a prompt is waiting for input, and any keystroke sent early is consumed by it
# rather than by the shell.
Type "namecom domain check lunarcrate.com"
Sleep 400ms
Enter

# Availability table, then the "Register …?" confirm appears.
Sleep 5s

# Answer the confirm. huh renders Yes/No with No selected; "y" picks Yes.
Type "y"
Sleep 800ms
Enter

# Registration, then the post-registration hints.
Sleep 6s

# Point the new domain somewhere.
Type "namecom dns create lunarcrate.com --type A --answer 1.2.3.4"
Sleep 400ms
Enter
Sleep 4s

Sleep 1500ms
