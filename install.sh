#!/bin/bash

# Fetch the latest release tag from the GitHub API
latest_release_url="https://api.github.com/repos/umputun/spot/releases/latest"
tag_name=$(curl -s "$latest_release_url" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

# Detect architecture
arch=$(uname -m)
case $arch in
    x86_64)
        arch="amd64"
        ;;
    aarch64)
        arch="arm64"
        ;;
    armv*)
        arch="arm"
        ;;
    *)
        echo "Unsupported architecture: $arch"
        exit 1
        ;;
esac

# Construct the download URL for the appropriate package
if [[ -f /etc/debian_version ]]; then
    pkg_type="deb"
elif [[ -f /etc/redhat-release ]]; then
    pkg_type="rpm"
else
    echo "Unsupported distribution"
    exit 1
fi

pkg_url="https://github.com/umputun/spot/releases/download/${tag_name}/spot_${tag_name}_linux_${arch}.${pkg_type}"

# Download and install the package
pkg_file="spot_${tag_name}_linux_${arch}.${pkg_type}"
wget -qO "$pkg_file" "$pkg_url"

if [[ $pkg_type == "deb" ]]; then
    sudo dpkg -i "$pkg_file"
    sudo apt-get -f install
else
    if command -v dnf > /dev/null; then
        sudo dnf install "$pkg_file"
    else
        sudo yum install "$pkg_file"
    fi
fi

# Clean up
rm "$pkg_file"
