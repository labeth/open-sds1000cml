// SPDX-License-Identifier: GPL-2.0
/* Experimental fixed-port EDMA drain. No FPGA programming, flash access, or
 * GPMC open/close: bind retains only a reference to the caller's inherited fd.
 * Exactly one acquisition owner must exclude the old userspace EDMA path.
 */
#include <linux/module.h>
#include <linux/fs.h>
#include <linux/file.h>
#include <linux/miscdevice.h>
#include <linux/slab.h>
#include <linux/dma-mapping.h>
#include <linux/completion.h>
#include <linux/mutex.h>
#include <linux/uaccess.h>
#include <linux/capability.h>
#include "edma-3.2.h"
#define CAPACITY 16384
#define TEST _IO('Q',0)
#define POP _IOW('Q',1,struct pop_arg)
#define BIND _IOW('Q',2,__u32)
struct pop_arg { __u32 bytes, user, reserved0, reserved1; };
struct session {
 struct completion done;
 struct mutex lock;
 void *buf;
 dma_addr_t phys;
 int channel;
 u16 dma_status;
 struct file *gpmc;
};
static atomic_t opened = ATOMIC_INIT(0);
static void dma_done(unsigned ch, u16 status, void *arg)
{
 struct session *s=arg;
 s->dma_status=status;
 complete(&s->done);
}
static int transfer(struct session *s, unsigned bytes, int test)
{
 struct edmacc_param p;
 int ret;
 memset(&p,0,sizeof(p));
 p.opt=SYNCDIM|TCINTEN|EDMA_TCC(s->channel);
 p.src=test ? s->phys : 0x01000032; /* CS1 selector 25, never configurable */
 p.dst=s->phys+CAPACITY;
 p.a_b_cnt=2|((bytes/2)<<16);
 p.src_dst_bidx=(2<<16)|(test ? 2 : 0);
 p.link_bcntrld=0xffff;
 p.ccnt=1;
 INIT_COMPLETION(s->done);
 s->dma_status=0;
 edma_write_slot(s->channel,&p);
 wmb();
 ret=edma_start(s->channel);
 if(ret) return ret;
 if(!wait_for_completion_timeout(&s->done,msecs_to_jiffies(100))) {
  edma_stop(s->channel);
  return -ETIMEDOUT; /* caller must not retry a partially advanced pop */
 }
 edma_stop(s->channel);
 rmb();
 return s->dma_status==DMA_COMPLETE ? 0 : -EIO;
}
static int read_reg(struct file *f, u16 reg, u16 *value)
{
 u16 a[3]={1,reg,0};
 mm_segment_t old=get_fs();
 long ret;
 set_fs(KERNEL_DS);
 ret=f->f_op->unlocked_ioctl(f,0x80026700,(unsigned long)a);
 set_fs(old);
 *value=a[2];
 return ret;
}
static long control(struct file *f,unsigned int cmd,unsigned long arg)
{
 struct session *s=f->private_data;
 struct pop_arg p;
 struct file *bus;
 u32 fd;
 u16 id,rev;
 int ret=0,i;
 if(!capable(CAP_SYS_RAWIO))return -EPERM;
 mutex_lock(&s->lock);
 switch(cmd) {
 case TEST:
  for(i=0;i<CAPACITY;i++)((u8*)s->buf)[i]=(i*37+(i>>8))&255;
  memset(s->buf+CAPACITY,0,CAPACITY);
  ret=transfer(s,CAPACITY,1);
  if(!ret && memcmp(s->buf,s->buf+CAPACITY,CAPACITY))ret=-EIO;
  break;
 case BIND:
  if(s->gpmc){ret=-EBUSY;break;}
  if(copy_from_user(&fd,(void __user *)arg,sizeof(fd))){ret=-EFAULT;break;}
  bus=fget(fd);
  if(!bus){ret=-EBADF;break;}
  if(strcmp(bus->f_path.dentry->d_name.name,"Gpmc") || !bus->f_op->unlocked_ioctl)ret=-EINVAL;
  else if(read_reg(bus,0,&id) || read_reg(bus,13,&rev) || id!=0x5a52 || rev!=11)ret=-ENODEV;
  if(ret)fput(bus);else s->gpmc=bus;
  break;
 case POP:
  if(!s->gpmc){ret=-ENODEV;break;}
  if(copy_from_user(&p,(void __user *)arg,sizeof(p))){ret=-EFAULT;break;}
  if(!p.bytes || p.bytes>CAPACITY || (p.bytes&1) || p.reserved0 || p.reserved1){ret=-EINVAL;break;}
  if(!access_ok(VERIFY_WRITE,(void __user *)(unsigned long)p.user,p.bytes)){ret=-EFAULT;break;}
  ret=transfer(s,p.bytes,0);
  if(!ret && copy_to_user((void __user *)(unsigned long)p.user,s->buf+CAPACITY,p.bytes))ret=-EFAULT;
  break;
 default: ret=-ENOTTY;
 }
 mutex_unlock(&s->lock);
 return ret;
}
static int open_session(struct inode *i,struct file *f)
{
 struct session *s;
 int ret;
 if(!capable(CAP_SYS_RAWIO))return -EPERM;
 if(atomic_cmpxchg(&opened,0,1))return -EBUSY;
 s=kzalloc(sizeof(*s),GFP_KERNEL);
 if(!s){ret=-ENOMEM;goto fail;}
 init_completion(&s->done);mutex_init(&s->lock);
 s->buf=dma_alloc_coherent(NULL,CAPACITY*2,&s->phys,GFP_KERNEL);
 if(!s->buf){ret=-ENOMEM;goto free_session;}
 s->channel=edma_alloc_channel(40,dma_done,s,EVENTQ_1);
 if(s->channel<0){ret=s->channel;goto free_buffer;}
 f->private_data=s;
 return 0;
free_buffer: dma_free_coherent(NULL,CAPACITY*2,s->buf,s->phys);
free_session: kfree(s);
fail: atomic_set(&opened,0);return ret;
}
static int close_session(struct inode *i,struct file *f)
{
 struct session *s=f->private_data;
 edma_stop(s->channel);edma_free_channel(s->channel);
 if(s->gpmc)fput(s->gpmc); /* inherited agent fd still holds its reference */
 dma_free_coherent(NULL,CAPACITY*2,s->buf,s->phys);kfree(s);
 atomic_set(&opened,0);return 0;
}
static const struct file_operations ops={.owner=THIS_MODULE,.open=open_session,.release=close_session,.unlocked_ioctl=control};
static struct miscdevice dev={.minor=MISC_DYNAMIC_MINOR,.name="acq_dma",.fops=&ops};
static int __init start(void){return misc_register(&dev);}
static void __exit stop(void){misc_deregister(&dev);}
module_init(start);module_exit(stop);
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("Experimental IRQ-driven owned-FPGA fixed CS1 pop DMA");
