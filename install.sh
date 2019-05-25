#!/bin/bash
#
#  Install go and termbacktime.
#
#  sudo ./install.sh <optional go version>
#
#  Linux:  i386, x86-64, ARMv6, ARMv8
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

if [ -x "$(command -v go)" ]; then
   echo "Go installed; installing termbacktime!"
   go version
   echo ""
   installtbt
  else
   GVERSION="1.12.5"
   if [ $1 ]; then
       GVERSION="$1"
   fi
   GOPATH="$HOME/go"
   GOROOT="/usr/local/go"

   echo "Go not found; Attempting to install v${GVERSION} to ${GOROOT} (\$GOPATH = ${GOPATH}), please wait..."

   ARCH=`uname -m`
   case "$ARCH" in
       i?86) ARCH="386" ;;
       x86_64) ARCH="amd64" ;;
       ARMv8) ARCH="arm64" ;;
       ARMv6) ARCH="armv6l" ;;
   esac
   DIST=`uname -s`
   case "$DIST" in
        Linux) DIST="linux" ;;
        Darwin) DIST="darwin" ;;
   esac
   GFILE="go$GVERSION.${DIST}-${ARCH}.tar.gz"

   if [ -d $GOROOT ]; then
       echo "Installation directories already exist $GOROOT"
       rm -rf "$GOROOT"
   fi

   mkdir -p "$GOROOT"
   chmod 777 "$GOROOT"

   wget --no-verbose https://storage.googleapis.com/golang/$GFILE -O $TMPDIR/$GFILE
   if [ $? -ne 0 ]; then
       echo "Go download failed! Exiting."
       exit 1
   fi

   tar -C "/usr/local" -xzf $TMPDIR/$GFILE

   if [ -f "$HOME/.gorc" ]; then
      source "$HOME/.gorc"
      sleep 1
    else
      touch "$HOME/.gorc"
      {
          echo '# Go paths'
          echo 'export PATH=$PATH:/usr/local/go/bin'
          echo 'export GOPATH=$HOME/go'
          echo 'export PATH=$PATH:$GOPATH/bin'
      } >> "$HOME/.gorc"
      touch "$HOME/.bashrc"
      {
         echo ""
         echo '# Source .gorc for Go paths'
         echo 'source $HOME/.gorc'
      } >> "$HOME/.bashrc"
      sleep 1
      source "$HOME/.gorc"
      sleep 1
   fi

   echo "GOROOT set to $GOROOT"
   mkdir -p "$GOPATH" "$GOPATH/src" "$GOPATH/pkg" "$GOPATH/bin" "$GOPATH/out"
   chmod 777 "$GOPATH" "$GOPATH/src" "$GOPATH/pkg" "$GOPATH/bin" "$GOPATH/out"
   echo "GOPATH set to $GOPATH"

   rm -f $TMPDIR/$GFILE

   if [ -x "$(command -v go)" ]; then
      echo "Go installed; installing termbacktime!"
      echo ""
      installtbt
    else
      echo "Go still not found! Could not install termbacktime."
   fi
fi
