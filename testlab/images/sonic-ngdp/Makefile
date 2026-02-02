VENDOR=cisco
NAME=sonic
IMAGE_FORMAT=qcow
IMAGE_GLOB=*.qcow2

# Expected image naming: sonic-vs-ngdp-<version>.qcow2
# Example: sonic-vs-ngdp-202411.qcow2 -> VERSION=ngdp-202411
# Docker tag: vrnetlab/cisco_sonic:ngdp-202411
VERSION=$(shell echo $(IMAGE) | sed -e 's/sonic-vs-//' | sed -e 's/.qcow2//')

-include ../makefile-sanity.include
-include ../makefile.include
