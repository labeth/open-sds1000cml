// Host-clock prefetch window over an explicitly owned published bank.
// pop is the GPMC slave's completed-read pulse, not its active read level.
// Common reset must also reset host RAM's response pipeline.
module acq_host_read_window(
 input wire clk,reset,select,select_bank,select_token,
 input wire [12:0] select_offset,input wire pop,release_request,clear_error,
 input wire [1:0] host_ready,host_token,input wire host_fault,
 input wire [11:0] host_words0,host_words1,
 output wire data_ready,exhausted,output wire [15:0] data,
 output reg selected=0,fault=0,output reg [12:0] cursor=0,
 output reg host_release=0,release_bank=0,release_token=0,
 output wire ram_read_enable,output wire [13:0] ram_read_halfword,
 input wire ram_read_valid,ram_read_error,input wire [15:0] ram_read_data
);
 reg bank=0,token=0,pending=0,buffer_valid=0;
 reg [12:0] limit=0;
 reg [15:0] buffered=0;
 wire owned=!host_fault && host_ready[bank] && host_token[bank]==token;
 wire [11:0] selected_words=select_bank ? host_words1 : host_words0;
 wire [12:0] selected_limit={selected_words,1'b0};
 wire selection_legal=!host_fault && host_ready[select_bank] && host_token[select_bank]==select_token &&
  selected_words!=0 && selected_words<=2560 && select_offset<selected_limit;
 assign data_ready=selected && owned && buffer_valid && !fault && !reset;
 assign data=data_ready ? buffered : 16'b0;
 assign exhausted=selected && owned && cursor==limit && !fault && !reset;
 assign ram_read_enable=selected && owned && !pending && !buffer_valid &&
  cursor<limit && !fault && !reset && !select && !release_request;
 assign ram_read_halfword=(bank ? 14'd5120 : 14'd0)+{1'b0,cursor};
 always @(posedge clk)begin
  host_release<=0;
  if(reset)begin
   selected<=0;fault<=0;cursor<=0;limit<=0;bank<=0;token<=0;
   pending<=0;buffer_valid<=0;release_bank<=0;release_token<=0;
  end else begin
   if(clear_error)fault<=0;
   if(ram_read_enable)pending<=1;
   if(ram_read_valid && pending)begin
    pending<=0;
    if(ram_read_error)begin fault<=1;buffer_valid<=0;end
    else if(selected && owned && !fault)begin buffered<=ram_read_data;buffer_valid<=1;end
   end
   if(selected && !owned)begin selected<=0;buffer_valid<=0;fault<=1;end
   if(pop)begin
    if(!data_ready)fault<=1;
    else begin cursor<=cursor+1'b1;buffer_valid<=0;end
   end
   if(select)begin
    if(pending || pop || release_request || !selection_legal)fault<=1;
    else begin
     selected<=1;bank<=select_bank;token<=select_token;
     cursor<=select_offset;limit<=selected_limit;buffer_valid<=0;
    end
   end
   if(release_request)begin
    if(!selected || !owned || pending || pop || select)fault<=1;
    else begin
     host_release<=1;release_bank<=bank;release_token<=token;
     selected<=0;buffer_valid<=0;
    end
   end
  end
 end
endmodule
