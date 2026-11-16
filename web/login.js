(() => {
  "use strict";

  const form = document.querySelector("#login-form");
  const password = document.querySelector("#password");
  if (!form || !password) return;

  const returnTo = form.querySelector('[name="return_to"]');
  const allowedAnchor = (pathname, hash) => {
    const patterns = {
      "/reports": /^#(?:task-[0-4]|report-reading|report-strategies|report-sources)$/,
      "/finance/profile": /^#(?:goals|risk[-_]profile|experience|liquidity[-_]days|restrictions|reserve[-_]current[-_]cents|reserve[-_]target[-_]cents|commitments[-_]30[-_]days[-_]cents|minimum[-_]checking[-_]cents|protected[-_]checking[-_]cents|confirmed)$/,
      "/dashboard": /^#connections-panel$/
    };
    return patterns[pathname]?.test(hash) === true;
  };
  if (returnTo) {
    try {
      const target = new URL(returnTo.value, window.location.origin);
      if (target.origin === window.location.origin && allowedAnchor(target.pathname, window.location.hash)) {
        returnTo.value = returnTo.value.split("#")[0] + window.location.hash;
      }
    } catch { /* The server validates the return target before redirecting. */ }
  }


  const togglePassword = document.querySelector("#toggle-password");
  const capsLock = document.querySelector("#caps-lock");
  const error = document.querySelector("#login-error");
  const errorText = document.querySelector("#login-error-text");
  const refresh = document.querySelector("#login-refresh");
  const status = document.querySelector("#login-status");
  const submit = form.querySelector("[type=submit]");
  const submitLabel = submit.querySelector(".submit-label");
  let submitting = false;

  togglePassword.hidden = false;
  togglePassword.addEventListener("click", () => {
    const show = password.type === "password";
    const start = password.selectionStart;
    const end = password.selectionEnd;
    password.type = show ? "text" : "password";
    togglePassword.setAttribute("aria-pressed", String(show));
    togglePassword.setAttribute("aria-label", show ? "Ocultar senha" : "Mostrar senha");
    if (start !== null && end !== null) password.setSelectionRange(start, end);
  });

  const updateCapsLock = (event) => {
    if (typeof event.getModifierState === "function") {
      const active = event.getModifierState("CapsLock");
      capsLock.hidden = !active;
      password.setAttribute("aria-describedby", active ? "caps-lock" : "password-help");
    }
  };
  password.addEventListener("keydown", updateCapsLock);
  password.addEventListener("keyup", updateCapsLock);
  password.addEventListener("blur", () => {
    capsLock.hidden = true;
    password.setAttribute("aria-describedby", "password-help");
  });
  password.addEventListener("input", () => {
    if (password.getAttribute("aria-invalid") === "true") {
      password.removeAttribute("aria-invalid");
      error.hidden = true;
    }
  });

  const fillDemo = document.querySelector("#fill-demo");
  if (fillDemo) {
    fillDemo.hidden = false;
    fillDemo.addEventListener("click", () => {
      password.value = "demo-conselho";
      password.dispatchEvent(new Event("input", { bubbles: true }));
      password.focus();
      status.textContent = "Senha de demonstração preenchida. Você já pode entrar.";
    });
  }

  if (window.fetch && window.URLSearchParams && window.FormData) {
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      if (submitting) return;
      submitting = true;
      let navigating = false;
      error.hidden = true;
      refresh.hidden = true;
      password.removeAttribute("aria-invalid");
      form.setAttribute("aria-busy", "true");
      submit.disabled = true;
      submitLabel.textContent = "Entrando…";
      status.textContent = "Verificando acesso.";

      try {
        const response = await fetch(form.action, {
          method: "POST",
          credentials: "same-origin",
          headers: {
            "Content-Type": "application/x-www-form-urlencoded;charset=UTF-8",
            "Accept": "application/json"
          },
          body: new URLSearchParams(new FormData(form))
        });
        const destination = new URL(response.url, window.location.href);
        if (response.redirected && response.ok && destination.origin === window.location.origin && ["/dashboard", "/reports", "/finance/profile"].includes(destination.pathname)) {
          if (returnTo) {
            const target = new URL(returnTo.value, window.location.origin);
            if (target.origin === destination.origin && target.pathname === destination.pathname && allowedAnchor(destination.pathname, target.hash)) destination.hash = target.hash;
          }
          navigating = true;
          status.textContent = "Acesso confirmado. Abrindo seu conselho.";
          window.location.assign(destination.href);
          return;
        }

        const result = await response.json().catch(() => null);
        errorText.textContent = result && typeof result.error === "string"
          ? result.error
          : "Não foi possível entrar. Tente novamente em instantes.";
        if (response.status === 401) {
          errorText.textContent = "Senha incorreta. Confira a senha e tente novamente.";
          password.setAttribute("aria-invalid", "true");
          password.focus();
          password.select();
        }
        refresh.hidden = response.status !== 403;
        error.hidden = false;
        status.textContent = "";
      } catch {
        errorText.textContent = "Não foi possível conectar. Confira a conexão e tente novamente.";
        error.hidden = false;
        status.textContent = "";
      } finally {
        if (!navigating) {
          submitting = false;
          form.removeAttribute("aria-busy");
          submit.disabled = false;
          submitLabel.textContent = "Entrar no conselho";
        }
      }
    });
  }

  const room = document.querySelector(".login-room");
  if (!room) return;
  const roomTitle = document.querySelector("#room-title");
  const roomCopy = document.querySelector("#room-copy");
  const instruction = document.querySelector("#room-instruction");
  const reset = document.querySelector("#room-reset");
  const hotspots = [...room.querySelectorAll("[data-room]")];
  const motionToggle = document.querySelector("#ambient-toggle");
  const motionPreference = document.querySelector("#motion-preference");
  const pan = room.querySelector(".room-pan");
  const spotlight = room.querySelector(".room-spotlight");
  const reducedMotion = window.matchMedia("(prefers-reduced-motion: reduce)");
  const finePointer = window.matchMedia("(hover: hover) and (pointer: fine)");
  const spaces = {
    overview: {
      title: "Seu contexto tem lugar.",
      copy: "Conversas e preferências se encontram no mesmo espaço, para você continuar de onde parou."
    },
    library: {
      title: "Memória para retomar.",
      copy: "Conversas e preferências ficam registradas. O contexto acompanha você entre o Telegram e o dashboard."
    },
    planning: {
      title: "Um ponto de partida.",
      copy: "As preferências compartilhadas ajudam a retomar o que foi combinado, mesmo quando você troca de canal."
    },
    council: {
      title: "Espaço para ponderar.",
      copy: "O CONSELHO reúne contexto para conversar sobre decisões. Nesta validação, todas as informações são fictícias."
    }
  };
  if (document.body.dataset.production === 'true') {
    Object.assign(spaces, {
      overview: {
        title: 'Seu escritório financeiro.',
        copy: 'Seus registros manuais ficam salvos no aplicativo. O movimento dos personagens é apenas visual.'
      },
      library: {
        title: 'Histórico para consultar.',
        copy: 'Consulte seus registros financeiros e a memória existente no aplicativo. A IA continua desativada.'
      },
      planning: {
        title: 'Os valores vêm de você.',
        copy: 'Cadastre gastos, organize categorias e informe ativos e dívidas para acompanhar o patrimônio.'
      },
      council: {
        title: 'Conheça os 23 personagens.',
        copy: 'Cada personagem tem seu lugar no escritório. O movimento é visual e não inicia análises.'
      }
    });
  }
  let paused = false;
  let inView = true;
  let frame = 0;
  let pointer = null;

  const resetPointer = () => {
    if (frame) window.cancelAnimationFrame(frame);
    frame = 0;
    pointer = null;
    room.dataset.pointer = "false";
    pan.style.removeProperty("--pan-x");
    pan.style.removeProperty("--pan-y");
  };
  const updateMotion = () => {
    const inactive = paused || reducedMotion.matches || document.hidden || !inView;
    room.dataset.ambientPaused = String(inactive);
    motionToggle.hidden = reducedMotion.matches;
    motionPreference.hidden = !reducedMotion.matches;
    motionToggle.setAttribute("aria-pressed", String(paused));
    motionToggle.querySelector("span").textContent = paused ? "Retomar movimento" : "Pausar movimento";
    if (inactive) resetPointer();
  };
  const setSpace = (key) => {
    const next = spaces[key] ? key : "overview";
    const details = spaces[next];
    room.dataset.focus = next;
    roomTitle.textContent = details.title;
    roomCopy.textContent = details.copy;
    hotspots.forEach((hotspot) => hotspot.setAttribute("aria-pressed", String(hotspot.dataset.room === next)));
    instruction.hidden = next !== "overview";
    reset.hidden = next === "overview";
  };
  hotspots.forEach((hotspot) => {
    hotspot.hidden = false;
    hotspot.addEventListener("click", () => {
      setSpace(room.dataset.focus === hotspot.dataset.room ? "overview" : hotspot.dataset.room);
    });
  });
  reset.addEventListener("click", () => {
    setSpace("overview");
    hotspots[0].focus({ preventScroll: true });
  });
  motionToggle.addEventListener("click", () => {
    paused = !paused;
    updateMotion();
  });

  room.addEventListener("pointermove", (event) => {
    if (!finePointer.matches || room.dataset.ambientPaused === "true" || event.pointerType === "touch") return;
    pointer = { x: event.clientX, y: event.clientY };
    if (frame) return;
    frame = window.requestAnimationFrame(() => {
      frame = 0;
      if (!pointer) return;
      const bounds = room.getBoundingClientRect();
      const x = Math.max(0, Math.min(1, (pointer.x - bounds.left) / bounds.width));
      const y = Math.max(0, Math.min(1, (pointer.y - bounds.top) / bounds.height));
      pan.style.setProperty("--pan-x", `${((x - 0.5) * -12).toFixed(2)}px`);
      pan.style.setProperty("--pan-y", `${((y - 0.5) * -8).toFixed(2)}px`);
      spotlight.style.setProperty("--spot-x", `${(x * 100).toFixed(1)}%`);
      spotlight.style.setProperty("--spot-y", `${(y * 100).toFixed(1)}%`);
      room.dataset.pointer = "true";
    });
  }, { passive: true });
  room.addEventListener("pointerleave", resetPointer);
  document.addEventListener("visibilitychange", updateMotion);
  if (typeof reducedMotion.addEventListener === "function") {
    reducedMotion.addEventListener("change", updateMotion);
    finePointer.addEventListener("change", resetPointer);
  }
  if ("IntersectionObserver" in window) {
    const observer = new IntersectionObserver(([entry]) => {
      inView = entry.isIntersecting;
      updateMotion();
    }, { threshold: 0.05 });
    observer.observe(room);
  }
  updateMotion();
  document.body.classList.add("login-ready");
})();
