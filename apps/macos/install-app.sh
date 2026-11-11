#!/usr/bin/env bash
# install-app.sh installs Lana.app and moves an existing lana install onto the
# binary inside it (spec #52). Without --yes it only prints what it would do.
#
#   1. Quit the running Lana.app from the target folder, keep the old bundle
#      as a zip (a second unzipped Lana.app would be one more LaunchServices
#      registration of the same bundle id), and copy the new bundle in place.
#   2. Refresh LaunchServices: forget registrations of Lana.app copies that are
#      gone (they can give System Settings a generic icon), register the new one.
#   3. Make ~/.local/bin/lana a symlink to Lana.app/Contents/MacOS/lana. A real
#      file there is kept as lana.pre-app. Scripts and the shell keep working,
#      and self-update (which resolves the symlink) updates the bundled binary.
#   4. Point ~/Library/LaunchAgents/dev.lana.daemon.plist at the bundled binary
#      and add AssociatedBundleIdentifiers, so Login Items shows lana with its
#      icon. The old plist is kept in the backup folder. A missing
#      plist is written from dev.lana.daemon.plist.in but not loaded.
#   5. Open Lana.app.
#
# It never stops or restarts the daemon. A running daemon keeps its process;
# it runs the bundled binary after its next restart (the printed commands).
#
# The dev app ("Lana Dev.app", bundle id dev.lana.menubar.dev) installs under
# its own name, and its CLI link and launchd agent are lana-dev and
# dev.lana.daemon.dev with LANA_HOME ~/.lana-dev, so it never touches the
# production app, CLI, or agent. make install-app-dev skips both anyway.
set -euo pipefail

usage() {
  cat <<'EOF'
usage: apps/macos/install-app.sh [--yes] [--app PATH] [--app-dir DIR]
                                 [--bin-dir DIR] [--agents-dir DIR] [--backup-dir DIR]
                                 [--no-cli-link] [--no-launchd] [--no-register] [--no-open]
  --yes           apply the steps (default: print them only)
  --app PATH      the built bundle (default: dist/Lana.app)
  --app-dir DIR   where Lana.app goes (default: /Applications)
  --bin-dir DIR   where the lana CLI symlink goes (default: ~/.local/bin)
  --agents-dir D  the LaunchAgents folder (default: ~/Library/LaunchAgents)
  --backup-dir D  where the previous Lana.app is zipped
                  (default: ~/Library/Application Support/lana-app)
  --no-cli-link   leave the lana CLI as it is
  --no-launchd    leave the launchd agent as it is
  --no-register   skip LaunchServices (for test installs)
  --no-open       do not open Lana.app at the end
EOF
}

here="$(cd "$(dirname "$0")" && pwd)"
repo="$(cd "$here/../.." && pwd)"
apply=0
src="$repo/dist/Lana.app"
app_dir="/Applications"
bin_dir="$HOME/.local/bin"
agents_dir="$HOME/Library/LaunchAgents"
backup_dir="$HOME/Library/Application Support/lana-app"
cli_link=1
launchd=1
register=1
open_app=1
lsregister="/System/Library/Frameworks/CoreServices.framework/Versions/A/Frameworks/LaunchServices.framework/Versions/A/Support/lsregister"

while [ $# -gt 0 ]; do
  case "$1" in
    --yes) apply=1 ;;
    --app) src="$2"; shift ;;
    --app-dir) app_dir="$2"; shift ;;
    --bin-dir) bin_dir="$2"; shift ;;
    --agents-dir) agents_dir="$2"; shift ;;
    --backup-dir) backup_dir="$2"; shift ;;
    --no-cli-link) cli_link=0 ;;
    --no-launchd) launchd=0 ;;
    --no-register) register=0 ;;
    --no-open) open_app=0 ;;
    -h|--help) usage; exit 0 ;;
    *) echo "install-app: unknown argument $1" >&2; usage >&2; exit 2 ;;
  esac
  shift
done

[ "$(uname -s)" = Darwin ] || { echo "install-app: needs macOS" >&2; exit 1; }

# run prints a step and runs it with --yes.
run() {
  printf '  $ %s\n' "$*"
  if [ "$apply" = 1 ]; then
    "$@"
  fi
}

src="${src%/}"
[ -d "$src/Contents/MacOS" ] || { echo "install-app: $src is not an app bundle; run make app first" >&2; exit 1; }
[ -x "$src/Contents/MacOS/lana" ] || { echo "install-app: $src has no Contents/MacOS/lana; rebuild with make app" >&2; exit 1; }
codesign --verify --strict "$src" || { echo "install-app: $src has an invalid signature" >&2; exit 1; }
bundle_id="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$src/Contents/Info.plist")"
variant="$(/usr/libexec/PlistBuddy -c 'Print :LanaVariant' "$src/Contents/Info.plist" 2>/dev/null || echo prod)"
app_home="$(/usr/libexec/PlistBuddy -c 'Print :LanaHome' "$src/Contents/Info.plist" 2>/dev/null || true)"
version="$("$src/Contents/MacOS/lana" --version | head -n 1)"
app_file="$(basename "$src")"
dest="${app_dir%/}/$app_file"
lana="$dest/Contents/MacOS/lana"
if [ "$variant" = dev ]; then
  label="dev.lana.daemon.dev"
  cli_name="lana-dev"
  [ "$backup_dir" = "$HOME/Library/Application Support/lana-app" ] && backup_dir="$HOME/Library/Application Support/lana-app-dev"
else
  label="dev.lana.daemon"
  cli_name="lana"
fi

echo "$app_file: $src"
echo "  bundle id $bundle_id, variant $variant${app_home:+, LANA_HOME $app_home}, daemon binary: $version"
[ "$apply" = 1 ] || echo "Dry run: nothing changes. Add --yes to apply."

echo "1. Install $dest"
if [ "$bundle_id" != "dev.lana.menubar" ] && [ "$bundle_id" != "dev.lana.menubar.dev" ]; then
  echo "  warning: $src has the test bundle id $bundle_id; build the installed app with plain make app"
fi
pid="$(pgrep -f "^$dest/Contents/MacOS/LanaMenu" || true)"
if [ -n "$pid" ]; then
  installed_id="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIdentifier' "$dest/Contents/Info.plist" 2>/dev/null || echo "$bundle_id")"
  echo "  quit the running Lana.app (pid $pid); the daemon keeps running"
  run osascript -e "tell application id \"$installed_id\" to quit"
  if [ "$apply" = 1 ]; then
    for _ in 1 2 3 4 5 6 7 8 9 10; do
      kill -0 "$pid" 2>/dev/null || break
      sleep 0.5
    done
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" || true
    fi
  fi
fi
run mkdir -p "$app_dir"
run rm -rf "$dest.new"
run ditto "$src" "$dest.new"
backup="$backup_dir/$app_file.previous.zip"
if [ -e "$dest" ]; then
  run mkdir -p "$backup_dir"
  run rm -f "$backup"
  run ditto -c -k --keepParent "$dest" "$backup"
  run rm -rf "$dest"
fi
run mv "$dest.new" "$dest"
run codesign --verify --strict "$dest"

echo "2. LaunchServices"
if [ "$register" = 1 ]; then
  while IFS= read -r stale; do
    [ -n "$stale" ] || continue
    [ -e "$stale" ] && continue
    echo "  forget $stale (gone)"
    run "$lsregister" -u "$stale"
  done < <("$lsregister" -dump 2>/dev/null | sed -n 's/^path: *\(.*\.app\) (0x[0-9a-f]*)$/\1/p' | awk -v name="/$app_file" 'substr($0, length($0) - length(name) + 1) == name' | sort -u)
  run "$lsregister" -f "$dest"
else
  echo "  skipped (--no-register)"
fi

echo "3. lana CLI"
link="$bin_dir/$cli_name"
if [ "$cli_link" = 0 ]; then
  echo "  skipped (--no-cli-link)"
elif [ -L "$link" ] && [ "$(readlink "$link")" = "$lana" ]; then
  echo "  $link already points at $lana"
else
  run mkdir -p "$bin_dir"
  if [ -e "$link" ] && [ ! -L "$link" ]; then
    echo "  keep the old binary as $link.pre-app"
    run mv "$link" "$link.pre-app"
  elif [ -L "$link" ]; then
    run rm "$link"
  fi
  run ln -s "$lana" "$link"
fi

echo "4. launchd agent $label"
plist="$agents_dir/$label.plist"
buddy=/usr/libexec/PlistBuddy
if [ "$launchd" = 0 ]; then
  echo "  skipped (--no-launchd)"
elif [ -f "$plist" ]; then
  current="$($buddy -c 'Print :ProgramArguments:0' "$plist" 2>/dev/null || true)"
  echo "  $plist runs ${current:-<none>}"
  run mkdir -p "$backup_dir"
  run cp -p "$plist" "$backup_dir/$label.plist.pre-app"
  run "$buddy" -c "Set :ProgramArguments:0 $lana" "$plist"
  if $buddy -c 'Print :AssociatedBundleIdentifiers' "$plist" >/dev/null 2>&1; then
    run "$buddy" -c 'Delete :AssociatedBundleIdentifiers' "$plist"
  fi
  run "$buddy" -c 'Add :AssociatedBundleIdentifiers array' -c "Add :AssociatedBundleIdentifiers:0 string $bundle_id" "$plist"
  if [ -n "$app_home" ]; then
    home_value="${app_home/#\~/$HOME}"
    $buddy -c 'Print :EnvironmentVariables' "$plist" >/dev/null 2>&1 || run "$buddy" -c 'Add :EnvironmentVariables dict' "$plist"
    if $buddy -c 'Print :EnvironmentVariables:LANA_HOME' "$plist" >/dev/null 2>&1; then
      run "$buddy" -c 'Delete :EnvironmentVariables:LANA_HOME' "$plist"
    fi
    run "$buddy" -c "Add :EnvironmentVariables:LANA_HOME string $home_value" "$plist"
  fi
  run plutil -lint "$plist"
else
  echo "  no agent yet: write $plist (not loaded)"
  path_value="$bin_dir:/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
  if [ "$apply" = 1 ]; then
    mkdir -p "$agents_dir" "$HOME/Library/Logs/lana"
    sed -e "s#@LANA@#$lana#g" -e "s#@HOME@#$HOME#g" -e "s#@PATH@#$path_value#g" -e "s#@BUNDLE_ID@#$bundle_id#g" \
      -e "s#<string>dev.lana.daemon</string>#<string>$label</string>#" \
      "$here/dev.lana.daemon.plist.in" > "$plist"
    if [ -n "$app_home" ]; then
      /usr/libexec/PlistBuddy -c "Add :EnvironmentVariables:LANA_HOME string ${app_home/#\~/$HOME}" "$plist"
    fi
    plutil -lint "$plist"
  fi
fi

echo "5. Open $app_file"
if [ "$open_app" = 1 ]; then
  run open "$dest"
else
  echo "  skipped (--no-open)"
fi

uid="$(id -u)"
echo
echo "Done$( [ "$apply" = 1 ] || echo " (dry run)" ). The daemon was not restarted."
if [ "$cli_link" = 0 ] && [ "$launchd" = 0 ]; then
  echo "The lana CLI and the launchd agent were left alone; the running daemon is not affected."
  echo "Undo: unzip $backup over $dest, or delete $dest."
  exit 0
fi
cat <<EOF
It runs $lana after its next restart:
  - Dashboard → Settings → Restart (waits for running turns), or
  - launchctl kickstart -k gui/$uid/$label (stops running turns now).
launchd reads the changed plist on the next login, or right away with:
  launchctl bootout gui/$uid/$label && launchctl bootstrap gui/$uid $plist
Undo: unzip $backup over $dest
(ditto -x -k "$backup" "${app_dir%/}"), move $link.pre-app back to $link, and
$backup_dir/$label.plist.pre-app back to $plist.
EOF
