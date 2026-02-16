#!/bin/bash
# build-alpine-testhost.sh — Build an Alpine Linux QCOW2 test host image.
#
# This script creates a minimal Alpine Linux VM image with pre-installed
# networking tools for data plane verification. Run on a machine with
# internet access; the resulting image requires no internet at runtime.
#
# Output: ~/.newtlab/images/alpine-testhost.qcow2
#
# Requirements: qemu-img, qemu-system-x86_64, curl, expect (or manual interaction)
#
# Usage:
#   ./tools/build-alpine-testhost.sh
#
# The script:
#   1. Downloads Alpine "virtual" ISO (optimized for VMs)
#   2. Creates a 1GB QCOW2 disk
#   3. Boots the ISO in QEMU with serial console
#   4. Runs setup-alpine with an answer file
#   5. Installs networking packages (iproute2, iperf3, tcpdump, hping3, etc.)
#   6. Configures SSH, serial console, and predictable interface names
#   7. Shuts down and exports the disk

set -euo pipefail

ALPINE_VERSION="3.19"
ALPINE_MINOR="3"
ALPINE_ISO="alpine-virt-${ALPINE_VERSION}.${ALPINE_MINOR}-x86_64.iso"
ALPINE_URL="https://dl-cdn.alpinelinux.org/alpine/v${ALPINE_VERSION}/releases/x86_64/${ALPINE_ISO}"

OUTPUT_DIR="${HOME}/.newtlab/images"
OUTPUT_IMG="${OUTPUT_DIR}/alpine-testhost.qcow2"
DISK_SIZE="1G"
RAM="512"

WORK_DIR=$(mktemp -d)
trap 'rm -rf "${WORK_DIR}"' EXIT

echo "=== Alpine Test Host Image Builder ==="
echo "Output: ${OUTPUT_IMG}"
echo "Work dir: ${WORK_DIR}"

# Download ISO if not cached
CACHE_DIR="${HOME}/.cache/newtlab"
mkdir -p "${CACHE_DIR}"
if [ ! -f "${CACHE_DIR}/${ALPINE_ISO}" ]; then
    echo "Downloading ${ALPINE_ISO}..."
    curl -L -o "${CACHE_DIR}/${ALPINE_ISO}" "${ALPINE_URL}"
else
    echo "Using cached ${ALPINE_ISO}"
fi

# Create disk
echo "Creating ${DISK_SIZE} QCOW2 disk..."
qemu-img create -f qcow2 "${WORK_DIR}/disk.qcow2" "${DISK_SIZE}"

# Create answer file for setup-alpine
cat > "${WORK_DIR}/answers" <<'ANSWERS'
KEYMAPOPTS="us us"
HOSTNAMEOPTS="-n testhost"
INTERFACESOPTS="auto lo
iface lo inet loopback

auto eth0
iface eth0 inet dhcp
"
TIMEZONEOPTS="-z UTC"
PROXYOPTS="none"
APKREPOSOPTS="-1"
SSHDOPTS="-c openssh"
NTPOPTS="-c chrony"
DISKOPTS="-m sys /dev/vda"
ANSWERS

# Create setup script that runs after first boot
cat > "${WORK_DIR}/setup.sh" <<'SETUP'
#!/bin/sh
set -e

# Enable community repo for hping3
sed -i 's|#\(.*/community\)|\1|' /etc/apk/repositories

# Install packages
apk update
apk add \
    openssh-server \
    dhcpcd \
    iproute2 \
    iputils-ping \
    iperf3 \
    tcpdump \
    hping3 \
    curl \
    bash \
    sudo

# Configure root password
echo "root:root" | chpasswd

# Enable SSH password auth for root
sed -i 's/^#PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config
sed -i 's/^PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config

# Enable serial console
sed -i 's|^#ttyS0|ttyS0|' /etc/inittab
grep -q ttyS0 /etc/inittab || echo "ttyS0::respawn:/sbin/getty -L ttyS0 115200 vt100" >> /etc/inittab

# Kernel cmdline: serial console + disable predictable interface names
# For Alpine with extlinux:
if [ -f /etc/update-extlinux.conf ]; then
    sed -i 's|^default_kernel_opts=.*|default_kernel_opts="console=ttyS0,115200 console=tty0 net.ifnames=0 biosdevname=0"|' /etc/update-extlinux.conf
    update-extlinux 2>/dev/null || true
fi
# For GRUB (if used):
if [ -f /etc/default/grub ]; then
    sed -i 's|^GRUB_CMDLINE_LINUX_DEFAULT=.*|GRUB_CMDLINE_LINUX_DEFAULT="console=ttyS0,115200 console=tty0 net.ifnames=0 biosdevname=0"|' /etc/default/grub
    grub-mkconfig -o /boot/grub/grub.cfg 2>/dev/null || true
fi

# Enable services at boot
rc-update add sshd default
rc-update add dhcpcd default

# Clean up
apk cache clean 2>/dev/null || true
rm -f /var/cache/apk/*

echo "=== Setup complete ==="
SETUP
chmod +x "${WORK_DIR}/setup.sh"

mkdir -p "${OUTPUT_DIR}"

echo ""
echo "=== Manual Installation Required ==="
echo ""
echo "The QEMU VM will start now with the Alpine ISO."
echo "At the login prompt, log in as 'root' (no password) and run:"
echo ""
echo "  1. setup-alpine                    # Install to disk"
echo "     - Use defaults for most options"
echo "     - Disk: vda, sys mode"
echo "     - When done, DON'T reboot yet"
echo ""
echo "  2. Mount and chroot to install packages:"
echo "     mount /dev/vda3 /mnt"
echo "     mount /dev/vda1 /mnt/boot"
echo "     mount -t proc proc /mnt/proc"
echo "     mount -t sysfs sys /mnt/sys"
echo "     mount -o bind /dev /mnt/dev"
echo "     chroot /mnt /bin/sh"
echo ""
echo "  3. In the chroot, run:"
echo "     sed -i 's|#\\(.*community\\)|\\1|' /etc/apk/repositories"
echo "     apk update"
echo "     apk add openssh-server dhcpcd iproute2 iputils-ping iperf3 tcpdump hping3 curl bash sudo"
echo "     echo 'root:root' | chpasswd"
echo "     sed -i 's/^#PermitRootLogin.*/PermitRootLogin yes/' /etc/ssh/sshd_config"
echo "     echo 'ttyS0::respawn:/sbin/getty -L ttyS0 115200 vt100' >> /etc/inittab"
echo "     rc-update add sshd default"
echo "     rc-update add dhcpcd default"
echo "     exit"
echo ""
echo "  4. poweroff"
echo ""
echo "Press Enter to start QEMU..."
read -r

qemu-system-x86_64 \
    -m "${RAM}" \
    -smp 1 \
    -drive file="${WORK_DIR}/disk.qcow2",if=virtio,format=qcow2 \
    -cdrom "${CACHE_DIR}/${ALPINE_ISO}" \
    -boot d \
    -nographic \
    -serial mon:stdio \
    -net nic,model=virtio \
    -net user

echo ""
echo "Compressing and copying image..."
qemu-img convert -c -O qcow2 "${WORK_DIR}/disk.qcow2" "${OUTPUT_IMG}"
echo ""
echo "=== Done ==="
echo "Image: ${OUTPUT_IMG}"
echo "Size: $(du -h "${OUTPUT_IMG}" | cut -f1)"
