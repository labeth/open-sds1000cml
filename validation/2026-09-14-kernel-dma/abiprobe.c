#include <linux/module.h>
#include <linux/fs.h>
#include <linux/file.h>
#include <linux/miscdevice.h>
#include <linux/dma-mapping.h>
#include <linux/completion.h>
#include <linux/mutex.h>
EXPORT_SYMBOL(fget);
EXPORT_SYMBOL(fput);
EXPORT_SYMBOL(misc_register);
EXPORT_SYMBOL(__init_waitqueue_head);
EXPORT_SYMBOL(__mutex_init);
EXPORT_SYMBOL(complete);
EXPORT_SYMBOL(wait_for_completion_timeout);
EXPORT_SYMBOL(dma_alloc_coherent);
EXPORT_SYMBOL(dma_free_coherent);

#include "../../app/internal/bus/kerneldma/edma-3.2.h"
EXPORT_SYMBOL(edma_alloc_channel);
EXPORT_SYMBOL(edma_free_channel);
EXPORT_SYMBOL(edma_write_slot);
EXPORT_SYMBOL(edma_start);
EXPORT_SYMBOL(edma_stop);
