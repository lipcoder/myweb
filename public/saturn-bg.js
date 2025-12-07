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

  const PARTICLE_COUNT = 260;
  let particles = [];
  let lastTime = 0;

  function resize() {
    width = canvas.width = window.innerWidth;
    height = canvas.height = window.innerHeight;

    centerX = width / 2;
    centerY = height * 0.55;

    const base = Math.min(width, height);
    ringRadius = base * 0.32;

    initParticles();
  }

  function initParticles() {
    particles = [];
    for (let i = 0; i < PARTICLE_COUNT; i++) {
      const angle = (i / PARTICLE_COUNT) * Math.PI * 2;
      const radiusOffset = (Math.random() - 0.5) * ringRadius * 0.18;
      const radius = ringRadius + radiusOffset;
      const depth = Math.random(); // 0 前景，1 背景
      const speed =
        (0.00014 + Math.random() * 0.00018) *
        (depth < 0.5 ? 1.25 : 0.9); // 近处稍快一点

      particles.push({
        angle,
        radius,
        depth,
        speed,
      });
    }
  }

  function update(dt) {
    const delta = dt || 16;
    for (let i = 0; i < particles.length; i++) {
      const p = particles[i];
      p.angle += p.speed * delta;
      if (p.angle > Math.PI * 2) {
        p.angle -= Math.PI * 2;
      }
    }
  }

  function drawParticle(p) {
    const x = centerX + Math.cos(p.angle) * p.radius;
    const y = centerY + Math.sin(p.angle) * p.radius * 0.34; // 压扁成椭圆

    const depthFactor = 1 - p.depth; // 越靠前越亮
    const size = 0.7 + depthFactor * 1.6;
    const alpha = 0.25 + depthFactor * 0.6;

    ctx.save();
    ctx.beginPath();
    ctx.fillStyle = "rgba(148, 163, 184," + alpha.toFixed(3) + ")";
    ctx.shadowColor = "rgba(56, 189, 248, " + (alpha * 0.7).toFixed(3) + ")";
    ctx.shadowBlur = 6 * depthFactor;
    ctx.arc(x, y, size, 0, Math.PI * 2);
    ctx.fill();
    ctx.restore();
  }

  function render() {
    ctx.clearRect(0, 0, width, height);

    // 背景渐变
    const bg = ctx.createRadialGradient(
      centerX,
      centerY,
      0,
      centerX,
      centerY,
      Math.max(width, height) * 0.9
    );
    bg.addColorStop(0, "rgba(15,23,42,0.9)");
    bg.addColorStop(1, "rgba(2,6,23,1)");
    ctx.fillStyle = bg;
    ctx.fillRect(0, 0, width, height);

    // 粒子环（分前后层，只是为了层次感，视觉上还是一个环）
    for (let i = 0; i < particles.length; i++) {
      const p = particles[i];
      if (Math.sin(p.angle) < 0) {
        drawParticle(p);
      }
    }

    for (let i = 0; i < particles.length; i++) {
      const p = particles[i];
      if (Math.sin(p.angle) >= 0) {
        drawParticle(p);
      }
    }
  }

  function loop(timestamp) {
    if (!lastTime) {
      lastTime = timestamp;
    }
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
