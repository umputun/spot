#!/bin/bash

# Fetch the latest release tag from the GitHub API
latest_release_url="https://api.github.com/repos/umputun/spot/releases/latest"
tag_name=$(curl -s "$latest_release_url" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

# Detect architecture
arch=$(uname -m)
case $arch in
    x86_64)
        if [[ "$(uname)" == "Darwin" ]]; then
            arch="macos_x86_64"
        else
            arch="amd64"
        fi
        ;;
    aarch64)
        if [[ "$(uname)" == "Darwin" ]]; then
            arch="macos_arm64"
        else
            arch="arm64"
        fi
        ;;
    armv*)
        arch="arm"
        ;;
    *)
        echo "Unsupported architecture: $arch"
        exit 1
        ;;
esac

# Detect distribution and set package type
if grep -iq 'debian' /etc/os-release; then
    pkg_type="deb"
elif grep -iq 'rhel' /etc/os-release; then
    pkg_type="rpm"
elif grep -iq 'alpine' /etc/os-release; then
    pkg_type="apk"
elif [[ "$(uname)" == "Darwin" ]]; then
    pkg_type="tar.gz"
else
    echo "Unsupported distribution"
    exit 1
fi

# Construct the download URL for the appropriate package
pkg_url="https://github.com/umputun/spot/releases/download/${tag_name}/spot_${tag_name}_${arch}.${pkg_type}"

# Download and install the package
pkg_file="spot_${tag_name}_${arch}.${pkg_type}"
wget -qO "$pkg_file" "$pkg_url"

if [[ $pkg_type == "deb" ]]; then
    sudo dpkg -i "$pkg_file"
    sudo apt-get -f install
elif [[ $pkg_type == "rpm" ]]; then
    if command -v dnf > /dev/null; then
        sudo dnf install "$pkg_file"
    else
        sudo yum install "$pkg_file"
    fi
elif [[ $pkg_type == "apk" ]]; then
    apk add --allow-untrusted "$pkg_file"
elif [[ $pkg_type == "tar.gz" ]]; then
    tar xzf "$pkg_file"
    mv spot spot-secrets /usr/local/bin/
fi

# Clean up
rm "$pkg_file"
