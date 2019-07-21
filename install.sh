#!/bin/bash
#
#  Locally install go and termbacktime.
#
#  ./install.sh <optional go version>
#
#  Linux + WSL:  i386, x86-64, ARMv6, ARMv8
#  Darwin: i386, x86-64
#
set -e

function installtbt() {
  echo "Fetching termbacktime..."
  echo ""
  go get -u -v github.com/termbacktime/termbacktime
  cd "$GOPATH/src/github.com/termbacktime/termbacktime"
  echo ""
  echo "Running make install..."
  make install
  echo ""
  termbacktime --version
}

if [ -n "`$SHELL -c 'echo $ZSH_VERSION'`" ]; then
    # assume Zsh
    shell_profile="zshrc"
elif [ -n "`$SHELL -c 'echo $BASH_VERSION'`" ]; then
    # assume Bash
    shell_profile="bashrc"
fi

if [ -x "$(command -v go)" ]; then
   echo "Go found: $(go version)"
   echo "Installing termbacktime to $GOPATH in 5 seconds..."
   sleep 5
   echo ""
   installtbt
  else
   GVERSION="1.12.7"
   if [ $1 ]; then
       GVERSION="$1"
   fi
   GOPATH="$HOME/go"
   GOROOT="$HOME/.goroot"
   TMPDIR=$(mktemp -d -t goinstall-XXXXXXXXXX)

   echo "Installing Go to $GOROOT in 5 seconds..."
   sleep 5
   echo "Attempting to install v${GVERSION} to ${GOROOT} (\$GOPATH = ${GOPATH}), please wait..."

   ARCHCASE=`uname -m`
   case "$ARCHCASE" in
       i?86) ARCH="386" ;;
       x86_64) ARCH="amd64" ;;
       ARMv8) ARCH="arm64" ;;
       ARMv6) ARCH="armv6l" ;;
   esac
   DISTCASE=`uname -s`
   case "$DISTCASE" in
        Linux) DIST="linux" ;;
        Darwin) DIST="darwin" ;;
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

   wget --no-verbose https://storage.googleapis.com/golang/$GFILE -O $TMPDIR/$GFILE
   if [ $? -ne 0 ]; then
       echo "Go download failed! Exiting."
       exit 1
   fi

   TMPEXT=$(mktemp -d -t go-extract-XXXXXXXXXX)
   tar -C "$TMPEXT" -xzf $TMPDIR/$GFILE
   mv $TMPEXT/go/* "$GOROOT"

   # Cleanup shell profile
   touch "$HOME/.${shell_profile}"
   sed -i '/# Golang paths/d' "$HOME/.${shell_profile}"
   sed -i '/export GOROOT/d' "$HOME/.${shell_profile}"
   sed -i '/:$GOROOT/d' "$HOME/.${shell_profile}"
   sed -i '/export GOPATH/d' "$HOME/.${shell_profile}"
   sed -i '/:$GOPATH/d' "$HOME/.${shell_profile}"
   {
       echo '# Golang paths'
       echo "export GOROOT=$GOROOT"
       echo 'export PATH=$PATH:$GOROOT/bin'
       echo 'export GOPATH=$HOME/go'
       echo 'export PATH=$PATH:$GOPATH/bin'
   } >> "$HOME/.${shell_profile}"

   echo "GOROOT set to $GOROOT"
   mkdir -p "$GOPATH" "$GOPATH/src" "$GOPATH/pkg" "$GOPATH/bin" "$GOPATH/out"
   chmod 755 "$GOPATH" "$GOPATH/src" "$GOPATH/pkg" "$GOPATH/bin" "$GOPATH/out"
   echo "GOPATH set to $GOPATH"

   echo "Running cleanup..."
   sleep 2
   source "$HOME/.${shell_profile}"
   rm -f $TMPDIR/$GFILE
   rm -rf $TMPEXT

   if [ -x "$(command -v go)" ]; then
      echo "Go installed; installing termbacktime!"
      echo ""
      installtbt
    else
      echo "Go still not found! Could not install termbacktime."
      echo "Please try sourcing your shell profile and running install again."
      echo -e "\n\tsource $HOME/.${shell_profile}\n"
   fi
fi
