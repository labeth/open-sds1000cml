// SPDX-License-Identifier: GPL-2.0
/* One-shot RAM-only qualification of the running kernel's EDMA IRQ API.
 * Does not open or read GPMC, allocate a character device, or program FPGA.
 */
#include <linux/module.h>
#include <linux/dma-mapping.h>
#include <linux/completion.h>
#include "edma-3.2.h"
#define SIZE 16384
static DECLARE_COMPLETION(done);
static u16 result;
static void callback(unsigned channel,u16 status,void *unused)
{result=status;complete(&done);}
static int __init start(void)
{
 void *buf;
 dma_addr_t phys;
 struct edmacc_param p;
 int ch,ret=0,i;
 buf=dma_alloc_coherent(NULL,SIZE*2,&phys,GFP_KERNEL);
 if(!buf)return -ENOMEM;
 ch=edma_alloc_channel(40,callback,NULL,EVENTQ_1);
 if(ch<0){ret=ch;goto free_mem;}
 for(i=0;i<SIZE;i++)((u8*)buf)[i]=(i*37+(i>>8))&255;
 memset(buf+SIZE,0,SIZE);memset(&p,0,sizeof(p));
 p.opt=SYNCDIM|TCINTEN|EDMA_TCC(ch);p.src=phys;p.dst=phys+SIZE;
 p.a_b_cnt=2|((SIZE/2)<<16);p.src_dst_bidx=2|(2<<16);
 p.link_bcntrld=0xffff;p.ccnt=1;
 edma_write_slot(ch,&p);wmb();ret=edma_start(ch);
 if(!ret && !wait_for_completion_timeout(&done,msecs_to_jiffies(100)))ret=-ETIMEDOUT;
 edma_stop(ch);rmb();
 if(!ret && (result!=DMA_COMPLETE || memcmp(buf,buf+SIZE,SIZE)))ret=-EIO;
 edma_free_channel(ch);
free_mem:
 dma_free_coherent(NULL,SIZE*2,buf,phys);
 return ret;
}
static void __exit stop(void){}
module_init(start);module_exit(stop);
MODULE_LICENSE("GPL");
MODULE_DESCRIPTION("One-shot RAM-only EDMA IRQ qualification, no GPMC access");
