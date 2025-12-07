// saturn-bg.js
(function () {
  const canvas = document.getElementById("saturn-bg");
  if (!canvas || !canvas.getContext) {
    return;
  }

  const ctx = canvas.getContext("2d");

  let width = 0;
  let height = 0;
  let centerX = 0;
  let centerY = 0;
  let ringRadius = 0;

  const STAR_COUNT = 240;
  const RING_LAYERS = 2;
  const RING_PARTICLES_PER_LAYER = 200;

  let stars = [];
  let ringParticles = [];
  let lastTime = 0;

  function resize() {
    width = canvas.width = window.innerWidth;
    height = canvas.height = window.innerHeight;

    // 环仍然偏下面一点
    centerX = width / 2;
    centerY = height * 0.58;
    ringRadius = Math.min(width, height) * 0.33;

    initStars();
    initRings();
  }

  function initStars() {
    stars = [];
    for (let i = 0; i < STAR_COUNT; i++) {
      const x = Math.random() * width;
      const y = Math.random() * height;
      const baseAlpha = 0.05 + Math.random() * 0.18;
      const phase = Math.random() * Math.PI * 2;
      const speed = 0.0004 + Math.random() * 0.0008;
      const size = 0.4 + Math.random() * 1.1;

      stars.push({ x, y, baseAlpha, phase, speed, size });
    }
  }

  function initRings() {
    ringParticles = [];

    for (let layer = 0; layer < RING_LAYERS; layer++) {
      const base = ringRadius * (0.9 + layer * 0.18);
      for (let i = 0; i < RING_PARTICLES_PER_LAYER; i++) {
        const angle = Math.random() * Math.PI * 2;
        const radiusJitter = (Math.random() - 0.5) * base * 0.16;
        const radius = base + radiusJitter;

        const depth =
          layer === 0 ? Math.random() * 0.5 : 0.4 + Math.random() * 0.6;

        // 内圈顺时针、外圈逆时针
        const dir = layer === 0 ? 1 : -1;
        const speed =
          dir *
          (0.00018 + Math.random() * 0.00022) *
          (depth < 0.5 ? 1.15 : 0.9);

        ringParticles.push({ angle, radius, depth, speed, layer });
      }
    }
  }

  function update(dt) {
    const delta = dt || 16;

    // 环粒子旋转
    for (let i = 0; i < ringParticles.length; i++) {
      const p = ringParticles[i];
      p.angle += p.speed * delta;
      if (p.angle > Math.PI * 2) p.angle -= Math.PI * 2;
      else if (p.angle < 0) p.angle += Math.PI * 2;
    }
  }

  // —— 背景：纯黑 —— 
  function drawBackground() {
    ctx.clearRect(0, 0, width, height);
    ctx.fillStyle = "#000000";
    ctx.fillRect(0, 0, width, height);
  }

  function drawStars() {
    for (let i = 0; i < stars.length; i++) {
      const s = stars[i];
      const alpha = s.baseAlpha + 0.3 * Math.sin(s.phase);

      ctx.save();
      ctx.beginPath();
      ctx.fillStyle =
        "rgba(148, 163, 184," + alpha.toFixed(3) + ")";
      ctx.arc(s.x, s.y, s.size, 0, Math.PI * 2);
      ctx.fill();
      ctx.restore();
    }
  }

  function drawRingParticle(p) {
    const x = centerX + Math.cos(p.angle) * p.radius;
    const y = centerY + Math.sin(p.angle) * p.radius * 0.34;

    const depthFactor = 1 - p.depth; // 越靠前越亮
    const baseSize = p.layer === 0 ? 1.1 : 0.9;
    const size = baseSize + depthFactor * 1.4;
    const alpha = 0.25 + depthFactor * 0.65;

    const tint =
      p.layer === 0
        ? { r: 125, g: 211, b: 252 } // 内圈偏亮偏蓝
        : { r: 148, g: 163, b: 184 }; // 外圈偏灰

    ctx.save();
    ctx.beginPath();
    ctx.fillStyle =
      "rgba(" +
      tint.r +
      "," +
      tint.g +
      "," +
      tint.b +
      "," +
      alpha.toFixed(3) +
      ")";
    ctx.shadowColor =
      "rgba(56, 189, 248," + (alpha * 0.7).toFixed(3) + ")";
    ctx.shadowBlur = 5 * depthFactor;
    ctx.arc(x, y, size, 0, Math.PI * 2);
    ctx.fill();
    ctx.restore();
  }

  function drawRings() {
    // 背面半圈
    for (let i = 0; i < ringParticles.length; i++) {
      const p = ringParticles[i];
      if (Math.sin(p.angle) < 0) {
        drawRingParticle(p);
      }
    }
    // 正面半圈
    for (let i = 0; i < ringParticles.length; i++) {
      const p = ringParticles[i];
      if (Math.sin(p.angle) >= 0) {
        drawRingParticle(p);
      }
    }
  }

  function render() {
    drawBackground();
    drawStars();
    drawRings();
  }

  function loop(timestamp) {
    if (!lastTime) lastTime = timestamp;
    const dt = timestamp - lastTime;
    lastTime = timestamp;

    update(dt);
    render();
    window.requestAnimationFrame(loop);
  }

  resize();
  window.addEventListener("resize", resize);
  window.requestAnimationFrame(loop);
})();
