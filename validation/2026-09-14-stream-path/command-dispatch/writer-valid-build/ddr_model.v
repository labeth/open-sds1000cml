`timescale 1ns/1ps
module altddio_out #(parameter width=1,power_up_high="OFF",intended_device_family="Cyclone IV E")
(input outclock,datain_h,datain_l,oe,aclr,aset,sclr,sset,outclocken,output dataout);
 reg low_latched=0,value=0;
 always @(posedge outclock or posedge aclr)
  if(aclr) begin low_latched<=0;value<=0;end
  else if(outclocken) begin low_latched<=datain_l;value<=datain_h;end
 always @(negedge outclock) if(!aclr && outclocken) value<=low_latched;
 assign dataout=oe ? value : 1'bz;
endmodule
