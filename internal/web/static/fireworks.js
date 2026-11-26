(() => {
  'use strict';
  const reduced = matchMedia('(prefers-reduced-motion: reduce)');
  let canvas, frame;

  function stop() {
    cancelAnimationFrame(frame);
    canvas?.remove();
    canvas = null;
  }

  function play() {
    stop();
    if (reduced.matches || document.hidden) return;
    canvas = document.createElement('canvas');
    const context = canvas.getContext('2d');
    if (!context) { canvas = null; return; }
    canvas.className = 'fireworks';
    canvas.setAttribute('aria-hidden', 'true');
    const width = document.documentElement.clientWidth, height = innerHeight;
    const ratio = Math.min(devicePixelRatio || 1, 2);
    canvas.width = width * ratio;
    canvas.height = height * ratio;
    context.scale(ratio, ratio);
    document.body.append(canvas);

    const colors = ['#ffc857', '#61c8ff', '#ff7bb0', '#78e0ad', '#b6a0ff'];
    const particles = [];
    for (let burst = 0; burst < 5; burst++) {
      const x = width * (.15 + Math.random() * .7);
      const y = height * (.15 + Math.random() * .35);
      for (let spark = 0; spark < 48; spark++) {
        const angle = Math.PI * 2 * spark / 48;
        const speed = (.07 + Math.random() * .13) * Math.min(width / 600, 1);
        particles.push({x, y, vx: Math.cos(angle) * speed, vy: Math.sin(angle) * speed,
          start: burst * 350, life: 900 + Math.random() * 400, color: colors[burst]});
      }
    }
    const started = performance.now();
    context.lineWidth = 2;
    context.lineCap = 'round';
    const draw = now => {
      const elapsed = now - started;
      context.clearRect(0, 0, width, height);
      for (const particle of particles) {
        const age = elapsed - particle.start;
        if (age < 0 || age > particle.life) continue;
        const tail = Math.max(0, age - 25);
        context.globalAlpha = (1 - age / particle.life) ** 2;
        context.strokeStyle = particle.color;
        context.beginPath();
        context.moveTo(particle.x + particle.vx * tail, particle.y + particle.vy * tail + .00006 * tail ** 2);
        context.lineTo(particle.x + particle.vx * age, particle.y + particle.vy * age + .00006 * age ** 2);
        context.stroke();
      }
      if (elapsed < 2800) frame = requestAnimationFrame(draw);
      else stop();
    };
    frame = requestAnimationFrame(draw);
  }

  reduced.addEventListener('change', stop);
  document.addEventListener('visibilitychange', stop);
  addEventListener('resize', stop);
  addEventListener('pagehide', stop);
  window.fireworks = {play, stop};
})();
