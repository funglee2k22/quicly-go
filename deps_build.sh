#!/bin/bash

echo "*********************************************"
echo "*****    QUICLY-GO DEPENDENCIES BUILD   *****"
echo "*********************************************"
echo

function assert_errorcode() {
  if [[ ! "$?" -eq "0" ]]; then
    echo "********************************"
    echo "**** RESULT: FAILURE        ****"
    echo "********************************"
    exit 1
  fi
}

function prerequisites_check() {
  echo [Prerequisites check: GO]
  go version
  assert_errorcode

  echo [Prerequisites check: cmake]
  cmake --version
  assert_errorcode

  echo [Prerequisites check: git]
  git version
  assert_errorcode
}

function prepare() {
  echo [Init submodules]
  git submodule init deps/openssl
  git submodule init deps/quicly
  git submodule init deps/c-for-go

  echo [Update submodules]
  git submodule update --init --recursive

  echo [Create gen directories]
  mkdir -p gen_openssl
  mkdir -p gen_quicly
}

function build_openssl() {
  echo [Build OpenSSL]
  pushd gen_openssl
  cmake ../deps/openssl -G"Unix Makefiles" -DCMAKE_INSTALL_PREFIX=$BASEDIR/internal/$GOOS/$GOARCH/bindings -DCMAKE_BUILD_TYPE=$BUILD
  assert_errorcode

  cmake --build .
  assert_errorcode

  cmake --build . --target install
  assert_errorcode
  popd
}

function build_quicly() {
  echo [Build Quicly]
  pushd gen_quicly
  cmake ../deps/quicly -G"Unix Makefiles" -DCMAKE_INSTALL_PREFIX=$BASEDIR/internal/$GOOS/$GOARCH/bindings -DOPENSSL_ROOT_DIR=$BASEDIR/internal/$GOOS/$GOARCH/bindings/include \
                                           -DCMAKE_BUILD_TYPE=$BUILD -DWITH_EXAMPLE=OFF
  assert_errorcode

  cmake --build .
  assert_errorcode

  cmake --build . --target install
  assert_errorcode
  popd
}

function reset() {
  cd $BASEDIR
  go clean -cache -x || true
  git reset --hard || true
  git clean -f -d || true
  exit 0
}

BASEDIR=$(dirname "$(realpath $0)")

BUILD="Release"

if [[ "$1" -eq "--help" ]]; then
  printf "Usage: build.bat [--debug][--clean]"
  exit 1
fi

if [[ "$1" -eq "--quic" ]]; then
  build_quicly
  exit 0
fi

if [[ "$1" -eq "--clean" ]]; then
  reset
  exit 0
fi
if [[ "$2" -eq "--clean" ]]; then
  reset
  exit 0
fi

if [[ "$1" -eq "--debug" ]]; then
  BUILD="Debug"
fi

prerequisites_check
assert_errorcode

GOOS=$(go env GOOS)
GOARCH=$(go env GOARCH)

prepare
assert_errorcode

build_openssl
assert_errorcode

build_quicly
assert_errorcode

echo
echo "***************************"
echo "****  RESULT: SUCCESS  ****"
echo "***************************"
cd $BASEDIR
exit 0
