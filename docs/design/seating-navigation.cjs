// Navigation follows the visible floor, furniture and open front thresholds.
// The background and sprites are never modified by this script.
const fs=require('node:fs'),path=require('node:path');
const target=path.resolve(__dirname,'../../assets/office/manifest.json');
const manifest=JSON.parse(fs.readFileSync(target,'utf8'));
manifest.world.walkable=[
  [180,390],[570,215],[930,315],[1080,325],[1210,390],[1340,385],[1400,430],[1370,485],[1280,555],
  [1435,820],[1440,930],[1220,1010],[830,1010],[650,800],[460,790],[340,880],[170,770]
];
manifest.world.obstacles=[
  // Council furniture; the carpet and empty floor behind its chairs remain passable.
  [[790,490],[820,390],[875,385],[930,410],[1015,370],[1075,385],[1135,430],[1195,600],[1150,670],[1070,700],[800,615]],
  // Legal desk, side glass and foreground planter. Its cutaway front is open.
  [[1285,658],[1370,615],[1535,680],[1535,800],[1455,780],[1310,730],[1285,728]],
  [[1195,590],[1285,640],[1285,800],[1195,740]],
  [[1425,730],[1505,755],[1535,815],[1510,895],[1430,880]],
  [[65,565],[270,540],[385,680],[420,800],[295,875],[100,780]],
  [[330,850],[565,755],[740,880],[755,1023],[570,1023],[350,955]],
  // Planning cabinetry, research desk and research side glass.
  [[565,120],[930,230],[938,345],[870,322],[860,285],[695,230],[565,210]],
  [[1250,255],[1290,220],[1535,310],[1535,438],[1440,415],[1420,370],[1260,315]],
  [[1170,225],[1188,230],[1188,377],[1170,370]]
];
manifest.world.seats.find(seat=>seat.id==='research-desk').approach={x:1344,y:432};
manifest.world.seats.find(seat=>seat.id==='legal-desk').approach={x:1368,y:804};
fs.writeFileSync(target,JSON.stringify(manifest,null,2)+'\n','utf8');
