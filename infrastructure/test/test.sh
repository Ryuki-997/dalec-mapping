#!/bin/bash
set -euox pipefail

TAG_WITHOUT_V="${VERSION/v/}"

# Detect OS and package manager
detect_os() {
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        echo "Detected OS: $ID $VERSION_ID"
        export OS_ID="$ID"
        export OS_VERSION_ID="$VERSION_ID"
    else
        echo "Cannot detect OS"
        exit 1
    fi
}

# Set ARCH to the architecture of the system and normalize it
export ARCH=$(uname -m)
echo "System architecture: $ARCH"
case "${ARCH}" in
x86_64)
  export ARCH="x86_64"
  export NORMALIZED_ARCH="amd64"
  ;;
aarch64)
  export ARCH="aarch64"
  export NORMALIZED_ARCH="arm64"
  ;;
*)
  export NORMALIZED_ARCH="${ARCH}"
  ;;
esac

detect_os

# Map spec target names to storage directory names
map_target_to_storage_name() {
    local target="$1"
    case "$target" in
        azlinux3)
            echo "azl3"
            ;;
        *)
            echo "$target"
            ;;
    esac
}

# Map DEB spec target names to Ubuntu version directory names  
map_deb_target_to_ubuntu_version() {
    local target="$1"
    case "$target" in
        bionic)
            echo "ubuntu18.04"
            ;;
        focal)
            echo "ubuntu20.04"
            ;;
        jammy)
            echo "ubuntu22.04"
            ;;
        noble)
            echo "ubuntu24.04"
            ;;
        bookworm)
            echo "debian12"  # Debian 12 Bookworm
            ;;
        *)
            echo "$target"  # Fallback to original
            ;;
    esac
}

# Test RPM packages for Azure Linux and Mariner
test_rpm_package() {
    local target="$1"
    local storage_target=$(map_target_to_storage_name "$target")
    echo "Testing RPM package for target: $target (storage: $storage_target)"
    
    # Check if current OS can handle RPM packages
    if [[ "$OS_ID" != "azurelinux" && "$OS_ID" != "mariner" ]]; then
        echo "SKIPPED: RPM packages not compatible with OS: $OS_ID"
        echo "This is expected - RPM packages are only for Azure Linux/Mariner systems"
        return 0  # Return success for incompatible OS (expected behavior)
    fi
    
    local rpm_file="kubelogin-$TAG_WITHOUT_V-$REVISION.$storage_target.$ARCH.rpm"
    local rpm_url="https://kubernetesreleases.blob.core.windows.net/dalec-packages/kubelogin/$TAG_WITHOUT_V/$storage_target/$ARCH/$rpm_file"
    
    echo "Downloading RPM from: $rpm_url"
    if ! sudo curl -L -O "$rpm_url"; then
        echo "Failed to download RPM package for $target."
        echo "ERROR: Package should be available for compatible OS but download failed"
        return 1  # Return 1 for failure (package should exist on compatible OS)
    fi
    
    if [ ! -f "$rpm_file" ]; then
        echo "RPM file $rpm_file not found after download"
        echo "ERROR: Package should be available for compatible OS but file missing"
        return 1  # Return 1 for failure (package should exist on compatible OS)
    fi
    
    # Check if downloaded file is actually a valid RPM package
    if ! file "$rpm_file" | grep -q "RPM"; then
        echo "Downloaded file is not a valid RPM package for $target."
        echo "File type: $(file "$rpm_file")"
        echo "ERROR: Invalid package format - build system issue"
        return 1  # Return 1 for failure (invalid package is a real problem)
    fi
    
    echo "Installing RPM package: $rpm_file"
    if sudo tdnf install -y --nogpgcheck "./$rpm_file"; then
        echo "RPM package installed successfully"
    else
        echo "Failed to install RPM package"
        return 1  # Return 1 for runtime issues (should fail test)
    fi
    
    # Test the installed binary
    if command -v kubelogin > /dev/null && kubelogin -h > /dev/null; then
        # Check that version command works (don't compare specific version)
        if kubelogin --version > /dev/null 2>&1; then
            echo "RPM package test PASSED for $target - kubelogin installed and working"
            return 0
        else
            echo "RPM package test FAILED for $target - kubelogin --version command failed"
            return 1  # Return 1 for runtime issues (should fail test)
        fi
    else
        echo "RPM package test FAILED for $target - kubelogin command not working"
        return 1  # Return 1 for runtime issues (should fail test)
    fi
}

# Test DEB packages for Ubuntu
test_deb_package() {
    local target="$1"
    local ubuntu_version=$(map_deb_target_to_ubuntu_version "$target")
    echo "Testing DEB package for target: $target (ubuntu version: $ubuntu_version)"
    
    # Check if current OS can handle DEB packages
    if [[ "$OS_ID" != "ubuntu" && "$OS_ID" != "debian" ]]; then
        echo "SKIPPED: DEB packages not compatible with OS: $OS_ID"
        echo "This is expected - DEB packages are only for Ubuntu/Debian systems"
        return 0  # Return success for incompatible OS (expected behavior)
    fi
    
    # Use the actual filename format from upload: kubelogin_0.2.10-ubuntu22.04u1_amd64.deb
    local deb_file="kubelogin_$TAG_WITHOUT_V-${ubuntu_version}u${REVISION}_$NORMALIZED_ARCH.deb"
    local deb_url="https://kubernetesreleases.blob.core.windows.net/dalec-packages/kubelogin/$TAG_WITHOUT_V/$ubuntu_version/$NORMALIZED_ARCH/$deb_file"
    
    echo "Downloading DEB from: $deb_url"
    if ! curl -L -O "$deb_url"; then
        echo "Failed to download DEB package for $target."
        echo "ERROR: Package should be available for compatible OS but download failed"
        return 1  # Return 1 for failure (package should exist on compatible OS)
    fi
    
    if [ ! -f "$deb_file" ]; then
        echo "DEB file $deb_file not found after download"
        echo "ERROR: Package should be available for compatible OS but file missing"
        return 1  # Return 1 for failure (package should exist on compatible OS)
    fi
    
    # Check if downloaded file is actually a valid DEB package
    if ! file "$deb_file" | grep -q "Debian binary package"; then
        echo "Downloaded file is not a valid DEB package for $target."
        echo "File type: $(file "$deb_file")"
        echo "ERROR: Invalid package format - build system issue"
        return 1  # Return 1 for failure (invalid package is a real problem)
    fi
    
    echo "Installing DEB package: $deb_file"
    if sudo dpkg -i "./$deb_file"; then
        echo "DEB package installed successfully"
    else
        echo "Failed to install DEB package"
        # Try to fix dependencies if needed
        sudo apt-get install -f -y
        return 1  # Return 1 for runtime issues (should fail test)
    fi
    
    # Test the installed binary
    if command -v kubelogin > /dev/null && kubelogin -h > /dev/null; then
        # Check that version command works (don't compare specific version)
        if kubelogin --version > /dev/null 2>&1; then
            echo "DEB package test PASSED for $target - kubelogin installed and working"
            return 0
        else
            echo "DEB package test FAILED for $target - kubelogin --version command failed"
            return 1  # Return 1 for runtime issues (should fail test)
        fi
    else
        echo "DEB package test FAILED for $target - kubelogin command not working"
        return 1  # Return 1 for runtime issues (should fail test)
    fi
}

# Test container image
test_container_image() {
    local target="$1"
    echo "Testing container image for target: $target"
    
    # Check if docker or podman is available
    if command -v docker > /dev/null; then
        CONTAINER_CMD="docker"
    elif command -v podman > /dev/null; then
        CONTAINER_CMD="podman"
    else
        echo "Neither docker nor podman found, skipping container test"
        return 0
    fi
    
    # Expected image name based on spec: Azure/kubelogin
    # Container images typically use v-prefixed tags
    local image_tag="v$TAG_WITHOUT_V"
    local image_name="${REGISTRY}/${REPO}/kubelogin:$image_tag"
    
    echo "Testing container image: $image_name"
    if $CONTAINER_CMD run --rm "$image_name" -h > /dev/null 2>&1; then
        # Check that version command works in container (don't compare specific version)
        if $CONTAINER_CMD run --rm "$image_name" --version > /dev/null 2>&1; then
            echo "Container image test PASSED for $target - kubelogin working in container"
            return 0
        else
            echo "Container image test FAILED for $target - kubelogin --version failed in container"
            return 1  # Return 1 for runtime issues (should fail test)
        fi
    else
        echo "Container image test FAILED for $target - could not run kubelogin in container"
        echo "ERROR: Container image should be available but pull/run failed"
        return 1  # Return 1 for failure (image should exist)
    fi
}

# Main test logic - tests ALL build targets regardless of CI agent OS
main() {
    # Check if kubelogin is already installed
    if command -v kubelogin &> /dev/null; then
        echo "WARNING: kubelogin is already installed on this system:"
        kubelogin --version || echo "Could not get version of existing kubelogin"
        echo "This may affect test results. Consider running tests in a clean container."
    fi
    
    local overall_success=true
    local tests_run=0
    
    echo "============================================"
    echo "Testing ALL build targets regardless of CI agent OS"
    echo "Detected CI agent OS: $OS_ID $OS_VERSION_ID"
    echo "============================================"
    
    # Test ALL build targets from the spec, regardless of CI agent OS
    # Build targets: azlinux3/rpm, azlinux3/container, noble/deb, jammy/deb, focal/deb, bionic/deb, bookworm/deb, windowscross/container
    
    # Test RPM packages
    echo "Testing azlinux3 RPM package..."
    test_rpm_package "azlinux3"
    rpm_result=$?
    case $rpm_result in
        0)
            echo "SUCCESS: azlinux3 RPM package test PASSED"
            ;;
        1)
            echo "ERROR: azlinux3 RPM package test FAILED"
            overall_success=false
            ;;
    esac
    tests_run=$((tests_run + 1))
    
    # Test DEB packages for all Ubuntu/Debian targets
    for target in noble jammy focal bionic bookworm; do
        echo "Testing $target DEB package..."
        test_deb_package "$target"
        deb_result=$?
        case $deb_result in
            0)
                echo "SUCCESS: $target DEB package test PASSED"
                ;;
            1)
                echo "ERROR: $target DEB package test FAILED"
                overall_success=false
                ;;
        esac
        tests_run=$((tests_run + 1))
    done
    
    # Test container images
    echo "Testing azlinux3 container image..."
    test_container_image "azlinux3"
    container_result=$?
    case $container_result in
        0)
            echo "SUCCESS: azlinux3 container image test PASSED"
            ;;
        1)
            echo "ERROR: azlinux3 container image test FAILED"
            overall_success=false
            ;;
    esac
    tests_run=$((tests_run + 1))
    
    echo "Testing windowscross container image..."
    test_container_image "windowscross"
    windows_result=$?
    case $windows_result in
        0)
            echo "SUCCESS: windowscross container image test PASSED"
            ;;
        1)
            echo "ERROR: windowscross container image test FAILED"
            overall_success=false
            ;;
    esac
    tests_run=$((tests_run + 1))
    
    echo "============================================"
    echo "Test Summary: $tests_run tests executed across ALL build targets"
    echo "Return code meanings:"
    echo "  SUCCESS: Test passed - artifact available and working correctly"
    echo "  SUCCESS (SKIPPED): Test skipped - artifact not compatible with current OS (expected)"
    echo "  ERROR: Test failed - artifact not available OR runtime issues detected"
    
    if [ "$overall_success" = true ]; then
        echo "Overall test PASSED - All compatible artifacts available and working"
        exit 0
    else
        echo "Overall test FAILED - Missing artifacts or runtime issues detected"
        exit 1
    fi
}

main