#!/usr/bin/env python3
"""Exercise native MCP lookups over stdio, without remote communication."""
import json,os,subprocess
import yaml
from inventory import ROOT
options=dict(modelPath=str(ROOT/'engmod.yml'),requirementsPath=str(ROOT/'model/requirements.yml'),designPath=str(ROOT/'model/views.yml'),repoRoot=str(ROOT))
requests=[dict(jsonrpc='2.0',id=1,method='initialize',params=dict(protocolVersion='2024-11-05',capabilities={},clientInfo=dict(name='sds-model-audit',version='1'),initializationOptions=options))]
for id,name,args in [(2,'model.list',dict(kind='hardware_item',max=100)),(3,'model.entity',dict(entityId='HW-FPGA')),(4,'requirements.get',dict(id='REQ-SDS-041')),(5,'trace.matrix',{})]:
 requests.append(dict(jsonrpc='2.0',id=id,method='tools/call',params=dict(name=name,arguments=args)))
encoded=b''
requirements = [r['id'] for r in yaml.safe_load((ROOT/'model/requirements.yml').read_text())['requirements']]
for requirement in requirements:
 requests.append(dict(jsonrpc='2.0',id=len(requests)+1,method='tools/call',params=dict(name='requirements.get',arguments=dict(id=requirement))))
for request in requests:
 body=json.dumps(request).encode();encoded+=f'Content-Length: {len(body)}\r\n\r\n'.encode()+body
p=subprocess.run(['go','run','./cmd/engmcp'],cwd=os.environ.get('ENGINEERING_MODEL_GO_DIR', str(ROOT.parents[2] / 'engineering-model-go')),env={**os.environ,'GOPROXY':'off'},input=encoded,capture_output=True,timeout=120,check=True)
data=p.stdout;responses=[]
while data:
 head,data=data.split(b'\r\n\r\n',1);size=int(head.split(b':',1)[1]);response=json.loads(data[:size]);data=data[size:];responses.append(response)
assert len(responses)==len(requests),responses
for x in responses:
 assert 'error' not in x,x
 assert not x.get('result',{}).get('isError'),x
by_id = {response['id']: response for response in responses}
for request in requests:
 if request.get('params', {}).get('name') != 'requirements.get': continue
 expected = request['params']['arguments']['id']
 content = by_id[request['id']]['result']['content']
 payload = json.loads(next(item['text'] for item in content if item.get('type') == 'text'))
 assert payload['requirement']['ID'] == expected, (expected, payload)
text=json.dumps(responses)
assert 'EP4CE10F17C8' in text,'MCP did not return FPGA part identity'
assert 'REQ-SDS-041' in text,'MCP did not return the requested requirement'
assert 'HW-ADC' in text,'MCP hardware inventory omitted ADC'
(ROOT/'generated/mcp-check.json').write_text(json.dumps(responses,indent=2)+'\n')
print('PASS: native MCP hardware list, entity, requirement and trace matrix lookups')
