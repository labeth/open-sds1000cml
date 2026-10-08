// dcinv — DMA cache-invalidate helper for the owned-FPGA EDMA drain.
// The AM3352 EDMA writes DRAM as a bus master (not through the CPU cache), so a
// reused CPU-cached buffer reads back stale data. This misc/char driver exposes an
// ioctl that invalidates the D-cache lines of a user buffer (by MVA, to PoC) so the
// CPU re-reads the fresh DMA data — enabling a fast REUSED buffer instead of a
// fresh-alloc-per-drain. It only invalidates cache; it cannot corrupt memory.
#include <linux/module.h>
#include <linux/fs.h>
#include <linux/cdev.h>
#include <linux/device.h>
#include <linux/uaccess.h>

struct dcinv_arg { unsigned long addr; unsigned long len; };
#define DCINV_INV _IOW('D', 1, struct dcinv_arg)

static dev_t devno;
static struct cdev c_dev;
static struct class *cls;

static long dcinv_ioctl(struct file *f, unsigned int cmd, unsigned long arg)
{
	struct dcinv_arg a;
	unsigned long p, start, end;
	if (cmd != DCINV_INV)
		return -ENOTTY;
	if (copy_from_user(&a, (void __user *)arg, sizeof(a)))
		return -EFAULT;
	start = a.addr & ~63UL;          /* 64-byte cache line (Cortex-A8) */
	end = a.addr + a.len;
	for (p = start; p < end; p += 64) /* DCIMVAC: invalidate by MVA to PoC */
		asm volatile("mcr p15, 0, %0, c7, c6, 1" : : "r"(p) : "memory");
	asm volatile("mcr p15, 0, %0, c7, c10, 4" : : "r"(0) : "memory"); /* CP15 DSB */
	return 0;
}

static const struct file_operations dcinv_fops = {
	.owner = THIS_MODULE,
	.unlocked_ioctl = dcinv_ioctl,
};

static int __init dcinv_init(void)
{
	if (alloc_chrdev_region(&devno, 0, 1, "dcinv"))
		return -1;
	cdev_init(&c_dev, &dcinv_fops);
	if (cdev_add(&c_dev, devno, 1))
		goto err_cdev;
	cls = class_create(THIS_MODULE, "dcinv");
	if (IS_ERR(cls))
		goto err_cls;
	device_create(cls, NULL, devno, NULL, "dcinv");
	return 0;
err_cls:
	cdev_del(&c_dev);
err_cdev:
	unregister_chrdev_region(devno, 1);
	return -1;
}

static void __exit dcinv_exit(void)
{
	device_destroy(cls, devno);
	class_destroy(cls);
	cdev_del(&c_dev);
	unregister_chrdev_region(devno, 1);
}

module_init(dcinv_init);
module_exit(dcinv_exit);
MODULE_LICENSE("GPL");
