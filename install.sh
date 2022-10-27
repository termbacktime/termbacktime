#!/bin/bash
#
#  Locally install Golang and (update) termbacktime.
#
#  ./install.sh <optional go version>
#
#  Linux + WSL:  i386, x86-64, ARMv6, ARMv8
#  Darwin: i386, x86-64, ARM64
#
set -e

DLINK="https://golang.org/dl"
REPO="https://github.com/termbacktime/termbacktime.git"

function installtbt () {
  if git rev-parse --git-dir > /dev/null 2>&1; then
    if make is-termbacktime-repo > /dev/null 2>&1; then
      echo "Updating $REPO..."
      git checkout master && git pull
      echo ""
      echo "Running: make install-upx..."
      make install-upx
      echo ""
      termbacktime --version
      exit 0
    fi
  fi
  temp_dir="$(mktemp -d)"
  echo "Cloning $REPO to $temp_dir..."
  git clone -q "${REPO}" "${temp_dir}"
  echo "Running: make install-upx..."
  cd "${temp_dir}/" && make install-upx
  echo ""
  termbacktime --version
}

if [ -n "`$SHELL -c 'echo $ZSH_VERSION'`" ]; then
	SHELL_PROFILE="zshrc"
elif [ -n "`$SHELL -c 'echo $BASH_VERSION'`" ]; then
	SHELL_PROFILE="bashrc"
fi

latest () {
	$* "$DLINK/?mode=json" | \
		grep -v -E 'go[0-9\.]+(beta|rc)' | \
		grep -E -o 'go[0-9\.]+' | \
		grep -E -o '[0-9]\.[0-9]+(\.[0-9]+)?' | \
		sort -V | uniq | tail -1
}

if command -v "wget" >/dev/null; then
	FETCH="wget -qO-"
elif command -v "curl" >/dev/null; then
	FETCH="curl --silent"
else
	echo "Missing both wget and curl!"
	exit 3
fi

if [ -x "$(command -v go)" ]; then
	echo "Go found: $(go version)"
	echo "Checking for updates..."
	LAST=$(latest "$FETCH")
	if echo "$LAST" | grep -q -E '[0-9]\.[0-9]+(\.[0-9]+)?'; then
		echo "Latest version: go$LAST"
		echo ""
		# XXX: Offer to upgrade golang
	fi;
	if [ -x "$(command -v termbacktime)" ]; then
		echo "termbacktime found: $(termbacktime --version)"
		echo "Updating in 5 seconds..."
	else
		echo "Installing in 5 seconds..."
	fi;
	sleep 5
	echo ""
	installtbt
else
	GVERSION="1.14"
	if [ $1 ]; then
		GVERSION="$1"
	else
		echo "Finding latest Go version..."
		LAST=$(latest "$FETCH")
		if echo "$LAST" | grep -q -E '[0-9]\.[0-9]+(\.[0-9]+)?'; then
			echo "Latest version: go$LAST"
			GVERSION=$LAST
		else
			echo "Could not find latest version, defaulting to $GVERSION"
		fi
	fi
	echo ""
	GOPATH="$HOME/go"
	GOROOT="$HOME/.goroot"
	TMPDIR=$(mktemp -d -t goinstall-XXXXXXXXXX)

	echo "Installing Go to $GOROOT in 5 seconds..."
	sleep 5
	echo "Attempting to install v${GVERSION} to ${GOROOT} (\$GOPATH = ${GOPATH}), please wait..."

	ARCHCASE=`uname -m`
	case "$ARCHCASE" in
		i* | .*386.*) ARCH="386" ;;
		x*) ARCH="amd64" ;;
		ARMv8 | AArch64) ARCH="arm64" ;;
		ARMv6 | ARMv7l?) ARCH="armv6l" ;;
	esac
	DISTCASE=`uname -s`
	case "$DISTCASE" in
			Linux) DIST="linux" ;;
			Darwin) DIST="darwin" ARCH="amd64" ;; # No 32-Bit support!
	esac
	GFILE="go$GVERSION.${DIST}-${ARCH}.tar.gz"

	if [ -z "$ARCH" ]; then
		echo "Detected invalid or missing OS architecture! Stopping."
		exit 1;
	fi
	if [ -z "$DIST" ]; then
		echo "Detected invalid or missing distribution! Stopping."
		exit 1;
	fi

	echo "Detected $DISTCASE ($ARCHCASE) - Downloading $GFILE"

	if [ -d $GOROOT ]; then
		echo "Installation directories already exist $GOROOT -- removing."
		rm -rf "$GOROOT"
	fi

	mkdir -p "$GOROOT"
	chmod 755 "$GOROOT"

	if command -v "wget" >/dev/null; then
		wget --no-verbose $DLINK/$GFILE -O $TMPDIR/$GFILE
	elif command -v "curl" >/dev/null; then
		curl --silent -o $TMPDIR/$GFILE $DLINK/$GFILE
	fi
	if [ $? -ne 0 ]; then
		echo "Go download failed! Exiting."
		exit 1
	fi

	TMPEXT=$(mktemp -d -t go-extract-XXXXXXXXXX)
	tar -C "$TMPEXT" -xzf $TMPDIR/$GFILE
	mv $TMPEXT/go/* "$GOROOT"

	# Cleanup shell profile
	touch "$HOME/.${SHELL_PROFILE}"
	if [ "$(uname)" == "Darwin" ]; then
		sed -i '' '/# Golang paths/d' "$HOME/.${SHELL_PROFILE}"
		sed -i '' '/export GOROOT/d' "$HOME/.${SHELL_PROFILE}"
		sed -i '' '/:$GOROOT/d' "$HOME/.${SHELL_PROFILE}"
		sed -i '' '/export GOPATH/d' "$HOME/.${SHELL_PROFILE}"
		sed -i '' '/:$GOPATH/d' "$HOME/.${SHELL_PROFILE}"
	else
		sed -i '/# Golang paths/d' "$HOME/.${SHELL_PROFILE}"
		sed -i '/export GOROOT/d' "$HOME/.${SHELL_PROFILE}"
		sed -i '/:$GOROOT/d' "$HOME/.${SHELL_PROFILE}"
		sed -i '/export GOPATH/d' "$HOME/.${SHELL_PROFILE}"
		sed -i '/:$GOPATH/d' "$HOME/.${SHELL_PROFILE}"
	fi
	{
		echo '# Golang paths'
		echo "export GOROOT=$GOROOT"
		echo 'export PATH=$PATH:$GOROOT/bin'
		echo 'export GOPATH=$HOME/go'
		echo 'export PATH=$PATH:$GOPATH/bin'
	} >> "$HOME/.${SHELL_PROFILE}"

	echo "GOROOT set to $GOROOT"
	mkdir -p "$GOPATH" "$GOPATH/src" "$GOPATH/pkg" "$GOPATH/bin" "$GOPATH/out"
	chmod 755 "$GOPATH" "$GOPATH/src" "$GOPATH/pkg" "$GOPATH/bin" "$GOPATH/out"
	echo "GOPATH set to $GOPATH"

	echo "Running cleanup..."
	sleep 2
	source "$HOME/.${SHELL_PROFILE}"
	rm -f $TMPDIR/$GFILE
	rm -rf $TMPEXT

	if [ -x "$(command -v go)" ]; then
		echo "Go installed; installing termbacktime!"
		echo ""
		installtbt
	else
		echo "Go still not found! Could not install termbacktime."
		echo "Please try sourcing your shell profile and running install again."
		echo -e "\n\tsource $HOME/.${SHELL_PROFILE}\n"
	fi
fi
