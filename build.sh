#!/bin/bash -xe
# See http://tldp.org/LDP/abs/html/options.html
# -x -> Print each command to stdout before executing it, expand commands
# -e -> Abort script at first error, when a command exits with non-zero status
#   (except in until or while loops, if-tests, list constructs)

# $GOPATH isn't set by default, nor do we have a usable Go workspace :'(
GOPATH="/var/app/current"
APP_BUILD_DIR="$GOPATH/src/image-server"     # We will build the app here
APP_STAGING_DIR="/var/app/staging"            # Current directory
DEP_VERSION="v0.3.2"                          # Use specific version for stability
ENV_VAR_PREFIX="IMAGE_SERVER_"

# Install dep, a Go dependency management tool, if not already installed or if
# the version does not match.
if ! hash dep 2> /dev/null ||\
    [[ $(dep version | awk 'NR==2{print $3}') != "$DEP_VERSION" ]]; then
    # /usr/local/bin is expected to be on $PATH.
    curl -L \
        -s https://github.com/golang/dep/releases/download/$DEP_VERSION/dep-linux-amd64 \
        -o /usr/local/bin/dep

    chmod +x /usr/local/bin/dep
fi

# Remove the $APP_BUILD_DIR just in case it was left behind in a failed build.
rm -rf $APP_BUILD_DIR

# Setup the application directory
mkdir -p $APP_BUILD_DIR

# mv all files to $APP_BUILD_DIR
# https://superuser.com/questions/62141/how-to-move-all-files-from-current-directory-to-upper-directory
mv * .[^.]* $APP_BUILD_DIR
cd $APP_BUILD_DIR

# Pull in dependencies into vendor/.
dep ensure

# Build the binary with jsoniter tag.
go build -o application main.go -tags=jsoniter .

# Modify permissons to make the binary executable.
chmod +x application
# Move the binary back to staging dir.
# Along with the configuration files.
mkdir $APP_STAGING_DIR/bin
# By default, `bin/application` is executed. This way, a Procfile isn't needed.
mv application $APP_STAGING_DIR/bin
cp -r config $APP_STAGING_DIR
# TODO: Fix the viper not working with env var
# Generate prod config from env variables
/opt/elasticbeanstalk/bin/get-config environment --output YAML | sed s/${ENV_VAR_PREFIX}//g > $APP_STAGING_DIR/config/prod.app.yaml
# Copy .ebextensions back to staging directory.
# cp -r .ebextensions $APP_STAGING_DIR

# Clean up.
rm -rf $APP_BUILD_DIR

echo "Build successful!!"