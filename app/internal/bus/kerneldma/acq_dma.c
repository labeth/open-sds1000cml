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
#include <linux/kthread.h>
#include <linux/delay.h>
#include <linux/sched.h>
#include <linux/ktime.h>
#include "edma-3.2.h"
#define CAPACITY 16384
struct stream_stats {u32 values[8];};
#define STATS _IOR('Q',4,struct stream_stats)
#define TEST _IO('Q',0)
#define POP _IOW('Q',1,struct pop_arg)
#define START _IOW('Q',3,struct stream_arg)
#define RING_SLOTS 32
struct stream_arg {u32 log,source,target,seconds;};
struct stream_slot {void *data;dma_addr_t phys;u64 first;u32 words;};
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
 struct task_struct *worker;
 wait_queue_head_t readers;
 struct stream_slot slots[RING_SLOTS];
 struct stream_arg stream;
 volatile u32 head,tail;
 volatile int finished,error;
 u16 capacity;
 ktime_t irq_time;
 u32 stats[8];

};
static atomic_t opened = ATOMIC_INIT(0);
static void dma_done(unsigned ch, u16 status, void *arg)
{
 struct session *s=arg;
 s->irq_time=ktime_get();
 s->dma_status=status;
 complete(&s->done);
}
static int transfer_to(struct session *s, unsigned bytes, int test, dma_addr_t destination)
{
 struct edmacc_param p;
 int ret;ktime_t began;u32 usec;
 memset(&p,0,sizeof(p));
 p.opt=SYNCDIM|TCINTEN|EDMA_TCC(s->channel);
 p.src=test ? s->phys : 0x01000032; /* CS1 selector 25, never configurable */
 p.dst=destination;
 p.a_b_cnt=2|((bytes/2)<<16);
 p.src_dst_bidx=(2<<16)|(test ? 2 : 0);
 p.link_bcntrld=0xffff;
 p.ccnt=1;
 INIT_COMPLETION(s->done);
 s->dma_status=0;
 edma_write_slot(s->channel,&p);
 wmb();
 began=ktime_get();ret=edma_start(s->channel);
 if(ret) return ret;
 if(!wait_for_completion_timeout(&s->done,msecs_to_jiffies(100))) {
  edma_stop(s->channel);
  return -ETIMEDOUT; /* caller must not retry a partially advanced pop */
 }
 usec=ktime_us_delta(s->irq_time,began);s->stats[0]=max(s->stats[0],usec);
 usec=ktime_us_delta(ktime_get(),s->irq_time);s->stats[1]=max(s->stats[1],usec);
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
static int write_reg(struct file *f,u16 reg,u16 value)
{
 u16 a[3]={1,reg,value};
 mm_segment_t old=get_fs();long ret;
 set_fs(KERNEL_DS);ret=f->f_op->unlocked_ioctl(f,0x40026701,(unsigned long)a);set_fs(old);
 return ret;
}
static int command(struct session *s,u16 op)
{
 u16 before,now;unsigned long until=jiffies+HZ;int ret;
 ret=read_reg(s->gpmc,1,&before);if(ret)return ret;
 ret=write_reg(s->gpmc,1,op);if(ret)return ret;
 do {
  ret=read_reg(s->gpmc,1,&now);if(ret)return ret;
  if((now^before)&512)return (now&384) ? -EIO : 0;
  usleep_range(10,20);
 }while(time_before(jiffies,until));
 return -ETIMEDOUT;
}
static int stream_setup(struct session *s)
{
 int ret;
 #define WR(r,v) do {ret=write_reg(s->gpmc,r,v);if(ret)return ret;}while(0)
 WR(19,3);WR(18,s->stream.log);
 WR(2,0xffef);WR(3,7);WR(4,17);WR(5,0);
 WR(6,s->stream.source ? 0 : 1);WR(7,128);
 #undef WR
 return command(s,1);
}
static int stream_worker(void *arg)
{
 struct session *s=arg;
 struct sched_param prio={.sched_priority=1};
 unsigned long until=jiffies+s->stream.seconds*HZ;
 u64 next=0,first;u16 flags,after,count,index,tmp;u32 bank,words,base,part;
 int ret,stopped=0,found;u32 stage=0,usec;ktime_t previous=ktime_get(),now;
 struct stream_slot *slot;
 memset(s->stats,0,sizeof(s->stats));
 ret=sched_setscheduler(current,SCHED_FIFO,&prio);
 if(!ret)ret=stream_setup(s);
 while(!ret && !kthread_should_stop()) {
  if(time_after_eq(jiffies,until)){ret=-ETIMEDOUT;break;}
  now=ktime_get();usec=ktime_us_delta(now,previous);previous=now;s->stats[3]=max(s->stats[3],usec);stage=1;
  ret=read_reg(s->gpmc,32,&flags);if(ret)break;s->stats[5]=flags;
  if(flags&64){ret=-EOVERFLOW;break;}
  if(!(flags&256)){ret=-ENODEV;break;}
  if(!(flags&3)) {
   if(stopped && (flags&128))break;
   usleep_range(25,50);continue;
  }
  if(s->head-s->tail>=RING_SLOTS){ret=-ENOSPC;break;}
  stage=2;found=0;
  for(bank=0;bank<2;bank++) {
   if(!(flags&(1<<bank)))continue;
   first=0;
   for(part=0;part<4;part++) {
    ret=read_reg(s->gpmc,35+bank*4+part,&tmp);if(ret)break;
    first|=(u64)tmp<<(16*part);
   }
   if(ret)break;
   if(first==next){found=1;break;}
  }
  if(ret)break;
  if(!found){ret=-EILSEQ;break;}
  ret=read_reg(s->gpmc,33+bank,&count);if(ret)break;
  if(!count || count>s->capacity/4){ret=-EINVAL;break;}
  words=2*count-((flags>>(4+bank))&1);base=bank*s->capacity/2;
  ret=write_reg(s->gpmc,16,base);if(ret)break;
  ret=read_reg(s->gpmc,17,&tmp);if(ret)break;
  ret=write_reg(s->gpmc,17,base*2);if(ret)break;
  slot=&s->slots[s->head%RING_SLOTS];
  stage=3;ret=transfer_to(s,words*4,0,slot->phys);if(ret)break;
  stage=4;ret=read_reg(s->gpmc,27,&index);if(ret)break;
  if(index!=(base*2+words*2)%(s->capacity*2)){ret=-EILSEQ;break;}
  stage=5;ret=read_reg(s->gpmc,32,&after);if(ret)break;s->stats[5]=after;
  if((after&64) || !(after&256) || !(after&(1<<bank)) || ((after^flags)&(4<<bank))){ret=-EOVERFLOW;break;}
  // Coherent RAM now owns all bytes; no CPU payload copy before release.
  stage=6;ret=write_reg(s->gpmc,20,bank|(((flags>>(2+bank))&1)<<1));if(ret)break;
  usec=ktime_us_delta(ktime_get(),s->irq_time);s->stats[2]=max(s->stats[2],usec);
  slot->first=next;slot->words=words;next+=words;
  smp_wmb();s->head++;s->stats[6]=max(s->stats[6],s->head-s->tail);s->stats[7]++;wake_up_interruptible(&s->readers);
  if(!stopped && next>=s->stream.target) {
   ret=command(s,5);stopped=1;
  }
 }
 if(!stopped || ret || kthread_should_stop())command(s,5);
 if(!ret && !kthread_should_stop())write_reg(s->gpmc,19,0);
 s->stats[4]=ret ? stage : 0;s->error=ret;smp_wmb();s->finished=1;wake_up_interruptible(&s->readers);
 // Keep the task alive until close joins it; never leave a dangling task pointer.
 while(!kthread_should_stop()) {
  set_current_state(TASK_INTERRUPTIBLE);
  if(!kthread_should_stop())schedule();
 }
 __set_current_state(TASK_RUNNING);
 return 0;
}
static ssize_t stream_read(struct file *f,char __user *dst,size_t size,loff_t *pos)
{
 struct session *s=f->private_data;
 struct stream_slot *slot;u32 header[4];int ret;size_t bytes;
 mutex_lock(&s->lock);
 if(!s->worker){ret=-ENODEV;goto out;}
 ret=wait_event_interruptible(s->readers,s->head!=s->tail || s->finished);
 if(ret)goto out;
 smp_rmb();
 if(s->head==s->tail){ret=s->error;goto out;}
 slot=&s->slots[s->tail%RING_SLOTS];bytes=slot->words*4;
 if(size<bytes+sizeof(header)){ret=-EMSGSIZE;goto out;}
 header[0]=(u32)slot->first;header[1]=slot->first>>32;header[2]=slot->words;header[3]=0;
 if(copy_to_user(dst,header,sizeof(header)) || copy_to_user(dst+sizeof(header),slot->data,bytes)){ret=-EFAULT;goto out;}
 smp_mb();s->tail++;ret=bytes+sizeof(header);
 out: mutex_unlock(&s->lock);return ret;
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
 if(s->worker && cmd!=STATS){ret=-EBUSY;goto out;}
 switch(cmd) {
 case STATS:
  if(!s->worker || !s->finished){ret=-EBUSY;break;}
  if(copy_to_user((void __user *)arg,s->stats,sizeof(s->stats)))ret=-EFAULT;
  break;
 case TEST:
  for(i=0;i<CAPACITY;i++)((u8*)s->buf)[i]=(i*37+(i>>8))&255;
  memset(s->buf+CAPACITY,0,CAPACITY);
  ret=transfer_to(s,CAPACITY,1,s->phys+CAPACITY);
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
 case START:
  if(!s->gpmc){ret=-ENODEV;break;}
  if(copy_from_user(&s->stream,(void __user *)arg,sizeof(s->stream))){ret=-EFAULT;break;}
  if(s->stream.log<8 || s->stream.log>20 || s->stream.source>1 || !s->stream.target || !s->stream.seconds || s->stream.seconds>30){ret=-EINVAL;break;}
  ret=read_reg(s->gpmc,1,&id);if(ret)break;
  if(!(id&4) || (id&8)){ret=-EBUSY;break;}
  ret=read_reg(s->gpmc,26,&s->capacity);if(ret)break;
  if(s->capacity!=4096 && s->capacity!=8192){ret=-EINVAL;break;}
  for(i=0;i<RING_SLOTS;i++) {
   if(!s->slots[i].data)s->slots[i].data=dma_alloc_coherent(NULL,CAPACITY,&s->slots[i].phys,GFP_KERNEL);
   if(!s->slots[i].data){ret=-ENOMEM;break;}
  }
  if(ret)break;
  s->worker=kthread_run(stream_worker,s,"acq-stream");
  if(IS_ERR(s->worker)){ret=PTR_ERR(s->worker);s->worker=NULL;}
  break;
 case POP:
  if(!s->gpmc){ret=-ENODEV;break;}
  if(copy_from_user(&p,(void __user *)arg,sizeof(p))){ret=-EFAULT;break;}
  if(!p.bytes || p.bytes>CAPACITY || (p.bytes&1) || p.reserved0 || p.reserved1){ret=-EINVAL;break;}
  if(!access_ok(VERIFY_WRITE,(void __user *)(unsigned long)p.user,p.bytes)){ret=-EFAULT;break;}
  ret=transfer_to(s,p.bytes,0,s->phys+CAPACITY);
  if(!ret && copy_to_user((void __user *)(unsigned long)p.user,s->buf+CAPACITY,p.bytes))ret=-EFAULT;
  break;
 default: ret=-ENOTTY;
 }
 out: mutex_unlock(&s->lock);
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
 init_completion(&s->done);mutex_init(&s->lock);init_waitqueue_head(&s->readers);
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
 struct session *s=f->private_data;int n;
 if(s->worker)kthread_stop(s->worker);
 for(n=0;n<RING_SLOTS;n++)if(s->slots[n].data)dma_free_coherent(NULL,CAPACITY,s->slots[n].data,s->slots[n].phys);
 edma_stop(s->channel);edma_free_channel(s->channel);
 if(s->gpmc)fput(s->gpmc); /* inherited agent fd still holds its reference */
 dma_free_coherent(NULL,CAPACITY*2,s->buf,s->phys);kfree(s);
 atomic_set(&opened,0);return 0;
}
static const struct file_operations ops={.owner=THIS_MODULE,.open=open_session,.release=close_session,.unlocked_ioctl=control,.read=stream_read};
static struct miscdevice dev={.minor=MISC_DYNAMIC_MINOR,.name="acq_dma",.fops=&ops};
static int __init start(void){
 BUILD_BUG_ON(sizeof(void*)!=4);
 BUILD_BUG_ON(offsetof(struct file,private_data)!=0x68);
 BUILD_BUG_ON(offsetof(struct file,f_op)!=0x10);
 BUILD_BUG_ON(offsetof(struct file,f_path)!=8);
 BUILD_BUG_ON(offsetof(struct dentry,d_name)!=0x14);
 BUILD_BUG_ON(offsetof(struct file_operations,unlocked_ioctl)!=0x20);
 BUILD_BUG_ON(sizeof(struct miscdevice)!=36);
 BUILD_BUG_ON(offsetof(struct miscdevice,fops)!=8);
 BUILD_BUG_ON(offsetof(struct miscdevice,parent)!=20);
 BUILD_BUG_ON(offsetof(struct miscdevice,this_device)!=24);
 return misc_register(&dev);
}
static void __exit stop(void){misc_deregister(&dev);}
module_init(start);module_exit(stop);
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("Experimental IRQ-driven owned-FPGA fixed CS1 pop DMA");
