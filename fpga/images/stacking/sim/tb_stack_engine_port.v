// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// Register-level stacking bench through the real SRAM transport. Accepts the
// tb_stack_edge_accumulator script; the record sits at a nonzero SRAM origin.
// TRLC-LINKS: REQ-SDS-141, REQ-SDS-043
module tb_stack_engine_port #(parameter BINS=16);
 reg host_clk=0,engine_clk=0,clk=0;
 always #6.25 host_clk=~host_clk;
 initial begin #1.3;forever #4 engine_clk=~engine_clk;end
 always #2 clk=~clk;wire sample_clk;assign #0.4 sample_clk=clk;
 reg wc=0,rp=0;reg [7:0] ws=0,rs=0;reg [15:0] wd=0;wire [15:0] rd;wire hit;
 wire grant_request;reg transport_owned=0;
 reg [31:0] record_id=3;reg [18:0] record_start=0;reg [19:0] record_words=0;reg frozen=1;
 wire transport_ready,transport_done,read_valid,command,command_read,command_discard,command_continue;
 wire [31:0] read_data;wire [18:0] position;wire [19:0] command_count;
 stack_engine_port #(.BINS(BINS)) dut(.host_clk(host_clk),.engine_clk(engine_clk),.transport_clk(clk),
 .wc(wc),.ws(ws),.wd(wd),.rp(rp),.rs(rs),.rd(rd),.hit(hit),.grant_request(grant_request),.transport_owned(transport_owned),
 .frozen(frozen),.record_id(record_id),.record_start(record_start),.record_words(record_words),
 .transport_ready(transport_ready),.transport_done(transport_done),.transport_read_valid(read_valid),.transport_read_data(read_data),.position(position),
 .command(command),.command_read(command_read),.command_discard(command_discard),.command_continue(command_continue),.command_count(command_count));
 wire [31:0] dq;wire k1,k2,g1;
 sram_transport #(.CONTINUOUS_ONLY(1)) transport(.clk(clk),.sample_clk(sample_clk),.reset(1'b0),.locked(1'b1),
 .command(command),.command_read(command_read),.command_discard(command_discard),.command_continue(command_continue),.command_count(command_count),
 .ready(transport_ready),.write_data(32'd0),.write_valid(1'b0),.write_stop(1'b1),.read_data(read_data),.read_valid(read_valid),
 .done(transport_done),.position(position),.dq(dq),.k1(k1),.k2(k2),.g1(g1));
 // Transport grant follows the request once the transport is idle, as top.v does.
 always @(posedge clk)transport_owned<=grant_request && (transport_owned || transport_ready);
 localparam ORIGIN=1234;
 reg [31:0] memory[0:(1<<19)-1],stage1=0,stage2=0;reg [18:0] address=0;
 assign dq=k1 && g1 ? stage2:32'bz;
 always @(posedge k2)if(g1)begin
  if(!k1)$fatal(1,"stacking wrote SRAM");
  stage1<=memory[address];stage2<=stage1;address<=address+1'b1;
 end
 always @(posedge clk)if(command && !transport_owned)$fatal(1,"command without grant");
 reg [15:0] wave[0:65535];
 task write(input [7:0] s,input [15:0] v);
 begin @(negedge host_clk);ws=s;wd=v;wc=1;@(negedge host_clk);wc=0;repeat(3)@(negedge host_clk);end
 endtask
 reg [15:0] value;
 task read(input [7:0] s);
 begin @(negedge host_clk);rs=s;#1;if(!hit)$fatal(1,"selector %0d not decoded",s);value=rd;rp=1;@(negedge host_clk);rp=0;repeat(3)@(negedge host_clk);end
 endtask
 integer cycles;
 task operation(input [3:0] op);
 begin
  write(112,op);cycles=0;if($test$plusargs("trace"))$display("%t op %0d",$time,op);
  read(112);while(value[0])begin read(112);cycles=cycles+1;if(cycles>5000000)$fatal(1,"operation %0d timeout",op);end
 end
 endtask
 task write32(input [7:0] s,input [31:0] v);begin write(s,v[15:0]);write(s+1,v[31:16]);end endtask
 integer fd,r,op,a0,a1,a2,a3,a4,a5,a6,a7,a8,a9,a10,a11,a12,i,n;reg [319:0] received;reg [4095:0] file_name;
 reg [31:0] hits,crossings,peeked,rejected;
 initial begin
  if(!$value$plusargs("wave=%s",file_name))$fatal(1,"missing +wave");
  $readmemh(file_name,wave);
  if(!$value$plusargs("samples=%d",n))$fatal(1,"missing +samples");
  for(i=0;i<(1<<19);i=i+1)memory[i]=32'hdeadbeef;
  // The transport counts its read-pipeline flush pulse; model SRAM address = position.
  for(i=0;i<n/2;i=i+1)memory[ORIGIN+i]={wave[2*i+1],wave[2*i]};
  record_start=ORIGIN;record_words=n/2;
  if(!$value$plusargs("input=%s",file_name))$fatal(1,"missing +input");
  fd=$fopen(file_name,"r");
  if($test$plusargs("trace"))$display("%t start",$time);
  read(113);if(value!=16'h5342)$fatal(1,"capability %h",value);
  read(114);if(value!=BINS)$fatal(1,"bins %0d",value);
  write(109,1);cycles=0;read(109);while(value[1]!=1)begin read(109);cycles=cycles+1;if(cycles>100)$fatal(1,"grant timeout");end
  if($test$plusargs("trace"))$display("%t granted",$time);
  operation(5);read(112);if(value[2] || !value[4])$fatal(1,"reset status %h",value);
  while(!$feof(fd))begin
   r=$fscanf(fd,"%s",op);
   if(r==1 && op=="s")begin
    r=$fscanf(fd,"%d %d %d %d %d %d %d %d %d %d %d %d %d",a0,a1,a2,a3,a4,a5,a6,a7,a8,a9,a10,a11,a12);
    if(a1!=n)$fatal(1,"script record length");
    write32(125,a0);write32(115,a2);write32(117,a3);write(114,{a12[1:0],a5[0],a4[0]});write(113,{a7[7:0],a6[7:0]});
    write(119,a8);write32(120,a9);write(122,a10);write32(123,a11);
    operation(1);read(112);
    read(115);hits[15:0]=value;read(116);hits[31:16]=value;read(117);crossings[15:0]=value;read(118);crossings[31:16]=value;
    read(122);rejected[15:0]=value;read(123);rejected[31:16]=value;
    read(112);$display("D %0d %0d %0d %0d",hits,crossings,value[2],rejected);
   end else if(r==1 && op=="q")begin
    r=$fscanf(fd,"%d %d %d %d %d",a0,a1,a2,a3,a4);
    write(98,a0);write(99,a1);write(100,a2);write32(101,a3);write32(103,a4);
   end else if(r==1 && op=="t")begin
    r=$fscanf(fd,"%d",a0);write(96,0);
    for(i=0;i<a0;i=i+1)begin r=$fscanf(fd,"%d",a1);write(97,a1);end
   end else if(r==1 && op=="p")begin
    r=$fscanf(fd,"%d",a0);write32(125,a0);operation(2);read(112);a1=value[2];
    read(119);peeked[15:0]=value;read(120);peeked[31:16]=value;$display("P %08x %0d",peeked,a1);
   end else if(r==1 && op=="r")begin
    r=$fscanf(fd,"%d %d",a0,a1);write(127,{a0[14:0],a1[0]});operation(3);read(112);if(value[2])$fatal(1,"tile read error");
    write(110,0);for(i=0;i<20;i=i+1)begin read(111);received[16*i+:16]=value;end
    $display("S %080x",received);
   end
  end
  // Round-trip one uploaded tile entry and read it back unchanged.
  write(110,0);for(i=0;i<20;i=i+1)write(111,i==19 ? 16'h0 : 16'h1111*(i%15+1));
  write(127,{15'd3,1'b1});operation(4);read(112);if(value[2])$fatal(1,"tile write error");
  operation(3);write(110,0);for(i=0;i<20;i=i+1)begin read(111);if(value!==(i==19 ? 16'h0 : 16'h1111*(i%15+1)))$fatal(1,"upload word %0d %h",i,value);end
  // Releasing the grant stops SRAM access: a scan must then fail cleanly.
  write(109,0);repeat(20)@(negedge host_clk);
  write32(125,0);operation(1);read(112);if(!value[2])$fatal(1,"scan without grant succeeded");
  // Revoking the grant during a scan aborts it with an error and a cleared stack.
  write(109,1);cycles=0;read(109);while(value[1]!=1)begin read(109);cycles=cycles+1;if(cycles>100)$fatal(1,"regrant timeout");end
  write32(125,0);write(112,1);repeat(40)@(negedge host_clk);write(109,0);
  cycles=0;read(112);while(value[0])begin read(112);cycles=cycles+1;if(cycles>100000)$fatal(1,"revoked scan hung");end
  if(!value[2] || !value[4])$fatal(1,"revoked scan status %h",value);
  write(127,{15'd3,1'b1});operation(3);if(dut.download_ram[0]!=0)$fatal(1,"revoked scan kept stack state");
  $display("PASS engine port");$finish;
 end
endmodule
