// ENGMODEL-OWNER-UNIT: FU-RTL-ACQ-TESTS
`timescale 1ns/1ps
// Executable scheduling experiment using synthesizable ingress storage.
// Ingress RAM runs at 125 MHz behind acknowledged bridges to the 250 MHz
// transport. The command scheduler remains a testbench, not a hardware controller.
// Exercises the actual transport and a two-stage synchronous SRAM model.
// TRLC-LINKS: REQ-SDS-048
module tb;
 parameter AW=19,BATCH=5120,FIFO=4608,HOST_DELAY=2500000,ROUNDS=16,READ_RESERVE=32,WRITE_DIV=1;
 localparam N=1<<AW;
 reg clk=0,ram_clk=0;always #2 clk=~clk;always #4 ram_clk=~ram_clk;
 wire sample_clk;assign #0.4 sample_clk=clk;
 reg reset=1,locked=1,command=0,command_read=0,command_discard=0,command_continue=0,write_stop=0;
 reg [AW:0] command_count=0;
 wire ready,write_ready,read_valid,done,k1,k2,g1;
 wire [31:0] read_data,dq;wire [AW-1:0] position;
 integer cycles=0,produced=0,consumed=0,max_fifo=0;
 reg source_on=0,allow_write=0;
 wire ingress_valid,ingress_fault,ingress_ready;
 wire [35:0] ingress_word;
 wire [$clog2(FIFO+11)-1:0] ingress_count;
 wire write_slot=cycles%WRITE_DIV==0;
 wire write_valid=allow_write && ingress_valid && write_slot;
 wire [31:0] write_data=ingress_word[31:0];
 localparam FIFO_ROWS=FIFO>=512 ? 512 : 2;
 sram_ingress_path #(.BANKS(FIFO/FIFO_ROWS),.ROWS(FIFO_ROWS)) ingress(
  .core(clk),.ram_clk(ram_clk),.reset(reset),
  .source_valid(source_on && cycles%128==0),.source_data({4'(produced),32'(N+produced)}),
  .source_ready(ingress_ready),.ready(allow_write && write_ready && write_slot),
  .valid(ingress_valid),.data_out(ingress_word),.pending(ingress_count),.fault(ingress_fault));
 sram_transport #(.AW(AW),.CONTINUOUS_ONLY(1)) dut(.*);
 reg [31:0] mem[0:N-1];reg [AW-1:0] address=0;
 reg [31:0] stage1=0,stage2=0;
 integer physical_writes=0,received=0,next_read=N-3*BATCH;
 integer i,round,write_position,read_before,cycle_before;
 always @(posedge clk) begin
  cycles<=cycles+1;
  if(source_on && cycles%128==0)produced<=produced+1;
  if(write_valid && write_ready)begin
   if(ingress_word[35:32] !== (consumed&15))$fatal(1,"queued marker lost alignment");
   consumed<=consumed+1;
  end
  if(produced-consumed>max_fifo)max_fifo<=produced-consumed;
  if(ingress_fault)$fatal(1,"ingress FIFO fault");
  if(produced-consumed>FIFO)$fatal(1,"ingress overflow %0d",produced-consumed);
 end
 assign dq=k1 && g1 ? stage2 : 32'bz;
 always @(posedge k2) if(g1) begin
  if(!k1) begin
   if(address!==(physical_writes%N) || dq!==N+physical_writes)
    $fatal(1,"write sequence/address: addr=%0d word=%h expected=%0d",address,dq,N+physical_writes);
   mem[address]<=dq;physical_writes=physical_writes+1;
  end else begin stage1<=mem[address];stage2<=stage1;end
  address<=address+1'b1;
 end
 always @(negedge clk) if(read_valid) begin
  if(read_data!==next_read)$fatal(1,"read %0d got %h expected %h",received,read_data,next_read);
  next_read=next_read+1;received=received+1;
 end
 task tick;begin @(posedge clk);#0.8;@(negedge clk);#0.8;end endtask
 task launch(input integer rd,input integer discard,input integer n);
 begin
  wait(ready);command_read=rd;command_discard=discard;command_count=n;command=1;tick;command=0;
 end endtask
 task seek(input integer target);
 integer distance;
 begin
  distance=(target+N-position)%N;
  if(distance!=0)begin launch(1,1,distance);wait(done);tick;end
  if(position!==address || position!=target)$fatal(1,"seek position drift");
 end endtask
 task start_writer;
 begin launch(0,0,0);allow_write=1;wait(write_ready);end
 endtask
 task stop_writer;
 begin
  write_stop=1;wait(done);tick;allow_write=0;write_stop=0;
  if(position!==address || physical_writes!=consumed)$fatal(1,"write pipeline not drained");
 end endtask
 initial begin
  for(i=0;i<N;i=i+1)mem[i]=i;
  tick;reset=0;source_on=1;
  for(round=0;round<ROUNDS;round=round+1)begin
   // Host stalls are serviced with the SRAM writer still active.
   start_writer;
   if(round==5)repeat(HOST_DELAY)tick; // 10 ms host delay
   while(N+consumed-next_read<BATCH+READ_RESERVE || produced-consumed>4)tick;
   stop_writer;write_position=position;read_before=received;cycle_before=cycles;
   seek(next_read%N);
   launch(1,0,BATCH);wait(done);tick;
   if(received-read_before!=BATCH)$fatal(1,"read batch count");
   seek(write_position);
   if(cycles-cycle_before>N+256)$fatal(1,"extra counter circuit");
   $display("round=%0d excursion_clocks=%0d pending=%0d backlog=%0d",round,cycles-cycle_before,produced-consumed,N+consumed-next_read);
  end
  start_writer;source_on=0;while(produced!=consumed)tick;stop_writer;
  if(physical_writes!=produced)$fatal(1,"lost source data");
  $display("PASS timeslice: read=%0d written=%0d max_ingress=%0d/%0d",received,physical_writes,max_fifo,FIFO);
  $finish;
 end
 initial begin #200000000;$fatal(1,"timeout");end
endmodule
