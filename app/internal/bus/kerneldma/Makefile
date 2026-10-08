obj-m := acq_dma_test.o
# Draft only: file/device ABI still needs inspection before loading.
ifdef EXPERIMENTAL_GPMC_DMA
obj-m += acq_dma.o
endif
ccflags-y := -fno-pic -fno-pie -fno-unwind-tables -fno-asynchronous-unwind-tables
