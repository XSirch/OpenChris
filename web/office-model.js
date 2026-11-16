(function (root, factory) {
  'use strict';
  const model = factory();
  if (typeof module === 'object' && module.exports) module.exports = model;
  if (root) root.ConselhoOfficeModel = model;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  'use strict';
  const finite = value => typeof value === 'number' && Number.isFinite(value);
  const point = value => value && finite(value.x) && finite(value.y);
  const clamp = (value, min, max) => Math.max(min, Math.min(max, value));
  const distance = (a, b) => Math.hypot(a.x - b.x, a.y - b.y);
  const localAsset = value => typeof value === 'string' && /^\/static\/office\/[a-zA-Z0-9._/-]+$/.test(value) && !value.includes('..');
  const polygon = value => Array.isArray(value) && value.length >= 3 && value.every(p => Array.isArray(p) && p.length === 2 && p.every(finite));

  function validateManifest(source) {
    if (!source || source.version !== 1) throw new Error('Versão do escritório inválida.');
    const world = source.world;
    if (!world || !finite(world.width) || !finite(world.height) || world.width <= 0 || world.height <= 0 || !polygon(world.walkable) || !Array.isArray(world.obstacles) || !world.obstacles.every(polygon)) throw new Error('Mapa de circulação inválido.');
    if (!localAsset(world.background)) throw new Error('O cenário deve usar um arquivo local do escritório.');
    if (!world.rooms || !['memory','goals','council','legal'].every(room => point(world.rooms[room]))) throw new Error('Áreas do escritório incompletas.');
    const objectIds = new Set();
    if (world.seats !== undefined) {
      const seatIds = new Set();
      const withinWorld = p => point(p) && p.x >= 0 && p.y >= 0 && p.x <= world.width && p.y <= world.height;
      if (!Array.isArray(world.seats) || world.seats.length > 16) throw new Error('Assentos do escritório inválidos.');
      for (const seat of world.seats) {
        if (!seat || typeof seat.id !== 'string' || !/^[a-z0-9-]+$/.test(seat.id) || seatIds.has(seat.id) || typeof seat.label !== 'string' || !seat.label.trim() || seat.label.length > 120 || !withinWorld(seat.position) || (seat.anchor !== undefined && !withinWorld(seat.anchor)) || !withinWorld(seat.approach) || ![-1,1].includes(seat.facing) || !Array.isArray(seat.occluders) || !seat.occluders.every(shape => polygon(shape) && shape.every(([x,y]) => withinWorld({x,y})))) throw new Error('Cada cadeira precisa de um assento exclusivo e uma aproximação válida.');
        if (seat.kind !== undefined && !['workstation','meeting'].includes(seat.kind) || seat.pose !== undefined && !['meeting-front','meeting-rear'].includes(seat.pose)) throw new Error('Tipo de assento ou pose inválidos.');
        seatIds.add(seat.id);
      }
    }
    if (world.meetingTable !== undefined && (!polygon(world.meetingTable.polygon) || !world.meetingTable.polygon.every(([x,y]) => x >= 0 && y >= 0 && x <= world.width && y <= world.height))) throw new Error('Mesa de reunião inválida.');
    if (world.financeScreen !== undefined) {
      const screen = world.financeScreen;
      if (!point(screen) || !finite(screen.width) || !finite(screen.height) || !finite(screen.shearY) || screen.width <= 0 || screen.height <= 0 || screen.x < 0 || screen.y + Math.min(0,screen.width*screen.shearY) < 0 || screen.x+screen.width > world.width || screen.y+screen.height+Math.max(0,screen.width*screen.shearY) > world.height) throw new Error('Tela financeira fora dos limites do escritório.');
    }
    if (world.interactives !== undefined) {
      if (!Array.isArray(world.interactives)) throw new Error('Objetos interativos inválidos.');
      for (const object of world.interactives) {
        const action = object?.action;
        const validAction = action && ((action.type === 'panel' && ['memory','goals','activity','finance'].includes(action.target)) || (action.type === 'agent' && /^A(0[1-9]|1[0-9]|2[0-3])$/.test(action.target)));
        if (!object || typeof object.id !== 'string' || !object.id.trim() || objectIds.has(object.id) || typeof object.label !== 'string' || !object.label.trim() || object.label.length > 160 || !polygon(object.polygon) || !object.polygon.every(([x,y]) => x >= 0 && y >= 0 && x <= world.width && y <= world.height) || !validAction) throw new Error('Objeto interativo inválido.');
        objectIds.add(object.id);
      }
    }
    if (!Array.isArray(source.atlases) || !source.atlases.length) throw new Error('O atlas de personagens está ausente.');
    const atlases = new Map();
    for (const atlas of source.atlases) {
      if (!atlas || typeof atlas.id !== 'string' || atlases.has(atlas.id) || !localAsset(atlas.url)) throw new Error('Cada atlas deve ter identidade e arquivo local próprios.');
      if (!finite(atlas.width) || !finite(atlas.height) || atlas.width <= 0 || atlas.height <= 0) throw new Error('Dimensões do atlas inválidas.');
      atlases.set(atlas.id, atlas);
    }
    if (!Array.isArray(source.agents) || source.agents.length !== 23) throw new Error('O escritório precisa dos 23 personagens.');
    const identities = new Set(), usedFrames = new Set();
    for (const agent of source.agents) {
      if (!agent || !/^A(0[1-9]|1[0-9]|2[0-3])$/.test(agent.id) || identities.has(agent.id)) throw new Error('Cada personagem precisa de uma identidade (ID) própria.');
      identities.add(agent.id);
      if (!['name','role','description'].every(field => typeof agent[field] === 'string' && agent[field].trim() && agent[field].length <= 2000) || !point(agent.home) || !finite(agent.height) || agent.height < 20 || agent.height > 240) throw new Error('Descrição ou posição de personagem inválida.');
      const atlas = atlases.get(agent.atlas);
      if (!atlas || !Array.isArray(agent.frames) || agent.frames.length !== 3) throw new Error('Cada personagem precisa de três quadros próprios no atlas.');
      for (const frame of agent.frames) {
        if (!frame || !['x','y','w','h'].every(key => Number.isInteger(frame[key])) || frame.x < 0 || frame.y < 0 || frame.w <= 0 || frame.h <= 0 || frame.x + frame.w > atlas.width || frame.y + frame.h > atlas.height) throw new Error('Um quadro ultrapassa os limites do atlas.');
        const key = [agent.atlas,frame.x,frame.y,frame.w,frame.h].join(':');
        if (usedFrames.has(key)) throw new Error('Os personagens devem usar quadros de sprite separados.');
        usedFrames.add(key);
      }
      if (agent.seated !== undefined) {
        const variants = agent.seated.variants || {};
        if (typeof variants !== 'object' || Object.keys(variants).some(key => !['meeting-front','meeting-rear'].includes(key))) throw new Error('Variações da pose sentada inválidas.');
        for (const seated of [agent.seated,...Object.values(variants)]) {
          const atlas = atlases.get(seated?.atlas), frame = seated?.frame;
          if (!atlas || !finite(seated.height) || seated.height < 20 || seated.height > agent.height || !frame || !['x','y','w','h'].every(key => Number.isInteger(frame[key])) || frame.x < 0 || frame.y < 0 || frame.w <= 0 || frame.h <= 0 || frame.x+frame.w > atlas.width || frame.y+frame.h > atlas.height) throw new Error('O quadro sentado ultrapassa os limites do atlas.');
          if (seated.anchor !== undefined && (!point(seated.anchor) || seated.anchor.x < 0 || seated.anchor.x > 1 || seated.anchor.y < 0 || seated.anchor.y > 1)) throw new Error('O ponto de apoio sentado precisa estar dentro do corpo.');
          if (seated.upperBody !== undefined && (!finite(seated.upperBody) || seated.upperBody < 0.4 || seated.upperBody > 0.9)) throw new Error('Recorte do corpo sentado inválido.');
          const key = [seated.atlas,frame.x,frame.y,frame.w,frame.h].join(':');
          if (usedFrames.has(key)) throw new Error('Cada personagem precisa de um quadro sentado próprio.');
          usedFrames.add(key);
        }
        for (const seat of world.seats || []) if (seat.pose && !variants[seat.pose]) throw new Error('Falta uma pose para a cadeira de reunião.');
      }
    }
    return JSON.parse(JSON.stringify(source));
  }

  function pointInPolygon(p, vertices) {
    let inside = false;
    for (let i = 0, j = vertices.length - 1; i < vertices.length; j = i++) {
      const a = {x:vertices[i][0],y:vertices[i][1]}, b = {x:vertices[j][0],y:vertices[j][1]};
      const cross = (p.y - a.y) * (b.x - a.x) - (p.x - a.x) * (b.y - a.y);
      if (Math.abs(cross) < 0.0001 && p.x >= Math.min(a.x,b.x) && p.x <= Math.max(a.x,b.x) && p.y >= Math.min(a.y,b.y) && p.y <= Math.max(a.y,b.y)) return true;
      if ((a.y > p.y) !== (b.y > p.y) && p.x < (b.x - a.x) * (p.y - a.y) / (b.y - a.y) + a.x) inside = !inside;
    }
    return inside;
  }

  function segmentsIntersect(a,b,c,d) {
    const cross = (p,q,r) => (q.x-p.x)*(r.y-p.y)-(q.y-p.y)*(r.x-p.x);
    if (Math.max(a.x,b.x) < Math.min(c.x,d.x) || Math.max(c.x,d.x) < Math.min(a.x,b.x) || Math.max(a.y,b.y) < Math.min(c.y,d.y) || Math.max(c.y,d.y) < Math.min(a.y,b.y)) return false;
    return cross(a,b,c)*cross(a,b,d) <= 0 && cross(c,d,a)*cross(c,d,b) <= 0;
  }

  function polygonTouchesCell(vertices, center, margin, probes) {
    if (probes.some(p => pointInPolygon(p,vertices)) || vertices.some(p => Math.abs(p[0]-center.x) <= margin && Math.abs(p[1]-center.y) <= margin)) return true;
    const corners = probes.slice(1);
    return vertices.some((p,index) => {
      const q = vertices[(index+1)%vertices.length], a = {x:p[0],y:p[1]}, b = {x:q[0],y:q[1]};
      return corners.some((c,corner) => segmentsIntersect(a,b,c,corners[(corner+1)%corners.length]));
    });
  }

  function createGrid(world, cellSize = 24) {
    const size = finite(cellSize) && cellSize >= 8 ? cellSize : 24;
    const cols = Math.ceil(world.width / size), rows = Math.ceil(world.height / size);
    const cells = new Uint8Array(cols * rows);
    for (let y = 0; y < rows; y++) {
      for (let x = 0; x < cols; x++) {
        const center = {x:(x + 0.5) * size,y:(y + 0.5) * size}, margin = size * 0.46;
        const probes = [center,{x:center.x-margin,y:center.y-margin},{x:center.x+margin,y:center.y-margin},{x:center.x+margin,y:center.y+margin},{x:center.x-margin,y:center.y+margin}];
        const inside = probes.every(p => p.x >= 0 && p.y >= 0 && p.x <= world.width && p.y <= world.height && pointInPolygon(p, world.walkable));
        const furniture = world.obstacles.some(vertices => polygonTouchesCell(vertices,center,margin,probes));
        cells[y * cols + x] = inside && !furniture ? 1 : 0;
      }
    }
    return {size, cols, rows, cells, width:world.width, height:world.height, world};
  }

  function cellIndex(grid, p) {
    if (!point(p) || p.x < 0 || p.y < 0 || p.x >= grid.width || p.y >= grid.height) return -1;
    return Math.floor(p.y / grid.size) * grid.cols + Math.floor(p.x / grid.size);
  }
  const cellPoint = (grid, index) => ({x:(index % grid.cols + 0.5) * grid.size,y:(Math.floor(index / grid.cols) + 0.5) * grid.size});
  function isWalkable(grid, p) {
    const index = cellIndex(grid, p);
    return index >= 0 && grid.cells[index] === 1 && pointInPolygon(p, grid.world.walkable) && !grid.world.obstacles.some(vertices => pointInPolygon(p, vertices));
  }
  function occupiedCells(grid, occupied) {
    const blocked = new Set();
    for (const p of occupied || []) {
      if (!point(p)) continue;
      const cx = Math.floor(p.x / grid.size), cy = Math.floor(p.y / grid.size);
      for (let y = cy - 1; y <= cy + 1; y++) for (let x = cx - 1; x <= cx + 1; x++) {
        if (x < 0 || y < 0 || x >= grid.cols || y >= grid.rows) continue;
        const index = y * grid.cols + x;
        if (distance(cellPoint(grid, index), p) < grid.size * 0.99) blocked.add(index);
      }
    }
    return blocked;
  }
  function nearestWalkable(grid, target, occupied = []) {
    if (!point(target)) return null;
    const blocked = occupiedCells(grid, occupied);
    let best = -1, bestDistance = Infinity;
    for (let index = 0; index < grid.cells.length; index++) {
      if (!grid.cells[index] || blocked.has(index)) continue;
      const currentDistance = distance(cellPoint(grid,index), target);
      if (currentDistance < bestDistance) { best = index; bestDistance = currentDistance; }
    }
    return best < 0 ? null : cellPoint(grid,best);
  }

  function findPath(grid, from, to, occupied = []) {
    if (!isWalkable(grid, from) || !isWalkable(grid, to)) return [];
    if (occupied.some(p => point(p) && distance(to,p) < grid.size*0.99)) return [];
    const start = cellIndex(grid,from), end = cellIndex(grid,to), blocked = occupiedCells(grid,occupied);
    blocked.delete(start);
    if (blocked.has(end)) return [];
    const open = new Set([start]), cameFrom = new Map(), cost = new Map([[start,0]]), estimate = new Map([[start,distance(from,to)]]);
    const directions = [[1,0],[-1,0],[0,1],[0,-1],[1,1],[-1,1],[1,-1],[-1,-1]];
    const available = (x,y) => x >= 0 && y >= 0 && x < grid.cols && y < grid.rows && grid.cells[y*grid.cols+x] && !blocked.has(y*grid.cols+x);
    while (open.size) {
      let current = -1, lowest = Infinity;
      for (const index of open) if (estimate.get(index) < lowest) { current = index; lowest = estimate.get(index); }
      if (current === end) {
        const route = [cellPoint(grid,current)];
        while (cameFrom.has(current)) { current = cameFrom.get(current); route.push(cellPoint(grid,current)); }
        route.reverse();
        route[0] = {x:from.x,y:from.y};
        if (distance(route[route.length-1],to) > 0.01) route.push({x:to.x,y:to.y});
        return route;
      }
      open.delete(current);
      const x = current % grid.cols, y = Math.floor(current / grid.cols);
      for (const [dx,dy] of directions) {
        if (!available(x+dx,y+dy) || (dx && dy && (!available(x+dx,y) || !available(x,y+dy)))) continue;
        const next = (y+dy)*grid.cols+x+dx, nextCost = cost.get(current) + Math.hypot(dx,dy)*grid.size;
        if (nextCost >= (cost.get(next) ?? Infinity)) continue;
        cameFrom.set(next,current); cost.set(next,nextCost); estimate.set(next,nextCost+distance(cellPoint(grid,next),to)); open.add(next);
      }
    }
    return [];
  }

  function fitCamera(world, viewport) {
    const scale = Math.min(viewport.width/world.width,viewport.height/world.height);
    return {x:(viewport.width-world.width*scale)/2,y:(viewport.height-world.height*scale)/2,scale};
  }
  function clampCamera(camera, world, viewport) {
    const fitted = fitCamera(world,viewport), scale = clamp(camera.scale || fitted.scale,fitted.scale,fitted.scale*4);
    const width = world.width*scale, height = world.height*scale;
    return {scale,x:width <= viewport.width ? (viewport.width-width)/2 : clamp(camera.x,viewport.width-width,0),y:height <= viewport.height ? (viewport.height-height)/2 : clamp(camera.y,viewport.height-height,0)};
  }
  const screenToWorld = (p,camera) => ({x:(p.x-camera.x)/camera.scale,y:(p.y-camera.y)/camera.scale});
  const worldToScreen = (p,camera) => ({x:p.x*camera.scale+camera.x,y:p.y*camera.scale+camera.y});
  function zoomCamera(camera, factor, anchor, world, viewport) {
    const before = screenToWorld(anchor,camera), fit = fitCamera(world,viewport), scale = clamp(camera.scale*factor,fit.scale,fit.scale*4);
    return clampCamera({scale,x:anchor.x-before.x*scale,y:anchor.y-before.y*scale},world,viewport);
  }
  const seatedSprite = agent => agent.seated?.variants?.[agent.seatPose] || agent.seated;
  function animationFrame(agent, elapsed, walking) {
    const seated = seatedSprite(agent);
    if (!walking && agent.pose === 'seated' && seated) return {index:0,rect:seated.frame,atlas:seated.atlas,pose:'seated'};
    const index = walking ? [1,0,2,0][Math.floor(Math.max(0,elapsed)/130)%4] : 0;
    return {index,rect:agent.frames[index],atlas:agent.atlas,pose:'standing'};
  }
  function spriteBounds(agent, frame) {
    const seated = seatedSprite(agent);
    const height = agent.pose === 'seated' && seated && frame === seated.frame ? seated.height : agent.height, width = height*frame.w/frame.h;
    if (agent.pose === 'seated' && seated?.anchor && agent.seatAnchor && frame === seated.frame) {
      const anchor = seated.anchor, x = agent.facing === -1 ? 1-anchor.x : anchor.x;
      return {x:agent.seatAnchor.x-width*x,y:agent.seatAnchor.y-height*anchor.y,width,height};
    }
    return {x:agent.x-width/2,y:agent.y-height,width,height};
  }
  function hitTest(agents, p) {
    for (const agent of [...agents].sort((a,b) => b.y-a.y)) {
      const bounds = spriteBounds(agent,animationFrame(agent,0,false).rect);
      if (agent.pose === 'seated' && agent.seatKind === 'meeting' && agent.seatMask && p.y > bounds.y+bounds.height*(seatedSprite(agent).upperBody || 1) && pointInPolygon(p,agent.seatMask)) continue;
      if (p.x >= bounds.x && p.x <= bounds.x+bounds.width && p.y >= bounds.y && p.y <= bounds.y+bounds.height) return agent;
    }
    return null;
  }

  function hitTarget(agents, objects, p) {
    const agent = hitTest(agents,p);
    if (agent) return {kind:'agent',agent};
    const object = objects.find(object => pointInPolygon(p,object.polygon));
    return object ? {kind:'object',object} : null;
  }

  function reduceWorkState(previous, event, ids) {
    if (!event || !['agent.started','agent.completed','agent.failed'].includes(event.event_type) || !event.payload || !ids.includes(event.payload.agent_id)) return previous;
    const payload = event.payload;
    if (!['running','idle','complete','completed','available','failed'].includes(payload.state)) return previous;
    const running = event.event_type === 'agent.started' && payload.state === 'running';
    const failed = event.event_type === 'agent.failed' || payload.state === 'failed';
    return {...previous,[payload.agent_id]:{state:running ? 'running' : failed ? 'failed' : 'available',task:running ? 'Processando evento registrado' : failed ? 'O processamento deste evento falhou' : 'Nenhuma tarefa em execução'}};
  }
  function reconcileWorkState(previous, snapshot) {
    if (!snapshot || snapshot.pending !== 0) return previous;
    const next = {...previous};
    for (const [id,work] of Object.entries(previous)) if (work.state === 'running') next[id] = {state:'available',task:'Nenhuma tarefa em execução'};
    return next;
  }
  function allocateSeats(agents, seats, work = {}) {
    const free = new Set(seats.map(seat => seat.id)), assigned = {};
    const sorted = [...agents].sort((a,b) => Number(work[b.id]?.state === 'running')-Number(work[a.id]?.state === 'running') || Number(Boolean(b.seatId))-Number(Boolean(a.seatId)) || a.id.localeCompare(b.id));
    for (const agent of sorted) {
      const seatId = free.has(agent.seatId) ? agent.seatId : free.values().next().value;
      if (seatId === undefined) break;
      assigned[agent.id] = seatId; free.delete(seatId);
    }
    return assigned;
  }
  return Object.freeze({validateManifest,pointInPolygon,createGrid,isWalkable,nearestWalkable,findPath,fitCamera,clampCamera,screenToWorld,worldToScreen,zoomCamera,seatedSprite,animationFrame,spriteBounds,hitTest,hitTarget,reduceWorkState,reconcileWorkState,allocateSeats});
});
