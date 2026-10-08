obj-m := dcinv.o
ccflags-y := -fno-unwind-tables -fno-asynchronous-unwind-tables

# Build against a matching linux-3.2 tree (see BUILD.md). Example:
#   make KDIR=/path/to/linux-3.2 CROSS_COMPILE=arm-buildroot-linux-gnueabihf-
KDIR ?= /path/to/linux-3.2
ARCH ?= arm

all:
	$(MAKE) -C $(KDIR) M=$(PWD) ARCH=$(ARCH) CROSS_COMPILE=$(CROSS_COMPILE) \
		KCFLAGS="-Wno-error -fno-pie" HOSTCFLAGS="-fcommon -Wno-error" modules

clean:
	$(MAKE) -C $(KDIR) M=$(PWD) clean
