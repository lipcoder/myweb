// saturn-bg.js
(function () {
  const canvas = document.getElementById("saturn-bg");
  if (!canvas || !canvas.getContext) return;

  const ctx = canvas.getContext("2d");

  let width = 0;
  let height = 0;
  let centerX = 0;
  let centerY = 0;
  let ringRadius = 0;

  let isSmallScreen = false;
  let particlesPerLayer = 0;

  const RING_LAYERS = 2;
  const BASE_PARTICLES_PER_LAYER = 240;

  let ringParticles = [];
  let lastTime = 0;

  function resize() {
    const dpr = window.devicePixelRatio || 1;

    width = window.innerWidth;
    height = window.innerHeight;

    const aspect = height / Math.max(width, 1);
    isSmallScreen = width < 700 || aspect > 1.3;

    // canvas 实际像素乘以 dpr，再把坐标系设回 CSS 像素
    canvas.width = width * dpr;
    canvas.height = height * dpr;
    ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

    centerX = width / 2;
    centerY = height * 0.58;
    ringRadius = Math.min(width, height) * (isSmallScreen ? 0.36 : 0.33);

    // 小屏稍微少一点点，防止太密集
    particlesPerLayer = isSmallScreen
      ? Math.round(BASE_PARTICLES_PER_LAYER * 0.85)
      : BASE_PARTICLES_PER_LAYER;

    initRings();
  }

  function initRings() {
    ringParticles = [];

    for (let layer = 0; layer < RING_LAYERS; layer++) {
      const base = ringRadius * (0.9 + layer * 0.16);

      for (let i = 0; i < particlesPerLayer; i++) {
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

    for (let i = 0; i < ringParticles.length; i++) {
      const p = ringParticles[i];
      p.angle += p.speed * delta;
      if (p.angle > Math.PI * 2) p.angle -= Math.PI * 2;
      else if (p.angle < 0) p.angle += Math.PI * 2;
    }
  }

  function drawBackground() {
    ctx.clearRect(0, 0, width, height);
    ctx.fillStyle = "#000000";
    ctx.fillRect(0, 0, width, height);
  }

  function drawRingParticle(p) {
    const x = centerX + Math.cos(p.angle) * p.radius;
    const y = centerY + Math.sin(p.angle) * p.radius * 0.34;

    const depthFactor = 1 - p.depth; // 越靠前越亮
    const baseSize = p.layer === 0 ? 1.0 : 0.85;

    // 小屏：粒子更小一点，整体更“细颗粒”
    const sizeScale = isSmallScreen ? 0.7 : 1.0;

    const size = (baseSize + depthFactor * 0.9) * sizeScale;
    const alpha = 0.45 + depthFactor * 0.4;

    const tint =
      p.layer === 0
        ? { r: 125, g: 211, b: 252 } // 内圈偏蓝
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

    // 不再使用任何阴影 / blur，纯色小圆点
    ctx.arc(x, y, size, 0, Math.PI * 2);
    ctx.fill();
    ctx.restore();
  }

  function drawRings() {
    // 背面半圈
    for (let i = 0; i < ringParticles.length; i++) {
      const p = ringParticles[i];
      if (Math.sin(p.angle) < 0) drawRingParticle(p);
    }
    // 正面半圈
    for (let i = 0; i < ringParticles.length; i++) {
      const p = ringParticles[i];
      if (Math.sin(p.angle) >= 0) drawRingParticle(p);
    }
  }

  function render() {
    drawBackground();
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
