// main.js

// 年份
document.getElementById("year").textContent = new Date().getFullYear();

// GitHub 头像/名字
(async function () {
  try {
    const resp = await fetch("https://api.github.com/users/lipcoder");
    if (!resp.ok) return;
    const data = await resp.json();
    const avatar = document.getElementById("gh-avatar");
    const name = document.getElementById("gh-name");
    if (data.avatar_url && avatar) avatar.src = data.avatar_url;
    if (data.name && name) name.textContent = data.name;
  } catch (e) {
    console.warn("GitHub API failed", e);
  }
})();

// 工具函数
async function fetchJSON(url) {
  const resp = await fetch(url);
  if (!resp.ok) throw new Error("HTTP " + resp.status);
  return await resp.json();
}

function formatTime(ts) {
  if (!ts) return "";
  const d = new Date(ts * 1000);
  const y = d.getFullYear();
  const m = String(d.getMonth() + 1).padStart(2, "0");
  const day = String(d.getDate()).padStart(2, "0");
  return `${y}-${m}-${day}`;
}

/* ---------------- 友链 ---------------- */
async function loadFriends() {
  const listEl = document.getElementById("friends-list");
  if (!listEl) return;
  listEl.textContent = "加载中…";

  try {
    const friends = await fetchJSON("/api/friends");
    listEl.textContent = "";

    if (!Array.isArray(friends) || friends.length === 0) {
      listEl.textContent = "暂时还没有友链，可以在本地 data/friends.json 中添加。";
      return;
    }

    friends.forEach((f) => {
      const item = document.createElement("div");
      item.className = "popover-item";

      const title = document.createElement("div");
      title.className = "popover-item-title";
      title.textContent = f.name || "未命名站点";

      const desc = document.createElement("div");
      if (f.desc) desc.textContent = f.desc;

      const meta = document.createElement("div");
      meta.className = "popover-item-meta";

      const link = document.createElement("a");
      link.href = f.url || "#";
      link.target = "_blank";
      link.rel = "noopener";
      link.textContent = "访问站点";

      const time = document.createElement("span");
      const t = formatTime(f.created_at);
      if (t) time.textContent = " · 收录于 " + t;

      meta.appendChild(link);
      if (t) meta.appendChild(time);

      item.appendChild(title);
      if (f.desc) item.appendChild(desc);
      item.appendChild(meta);

      listEl.appendChild(item);
    });
  } catch (e) {
    console.error(e);
    listEl.textContent = "加载友链失败，请检查后端或 data/friends.json。";
  }
}

/* ---------------- 留言板 ---------------- */
async function loadGuestbook() {
  const listEl = document.getElementById("guestbook-list");
  if (!listEl) return;
  listEl.textContent = "加载中…";

  try {
    const data = await fetchJSON("/api/guestbook");
    listEl.textContent = "";

    const items = Array.isArray(data) ? data : [];
    if (items.length === 0) {
      listEl.textContent = "还没有留言，快来抢个沙发～";
      return;
    }

    // 按时间倒序，只展示前 8 条
    items
      .slice()
      .sort((a, b) => (b.created_at || 0) - (a.created_at || 0))
      .slice(0, 8)
      .forEach((g) => {
        const item = document.createElement("div");
        item.className = "popover-item";

        const title = document.createElement("div");
        title.className = "popover-item-title";
        title.textContent = g.nickname || "匿名";

        const content = document.createElement("div");
        content.textContent = g.content || "";

        const meta = document.createElement("div");
        meta.className = "popover-item-meta";
        const t = formatTime(g.created_at);
        meta.textContent = t ? `留言时间 ${t}` : "";

        item.appendChild(title);
        item.appendChild(content);
        if (meta.textContent) item.appendChild(meta);

        listEl.appendChild(item);
      });
  } catch (e) {
    console.error(e);
    listEl.textContent = "加载留言失败，请检查后端或 data/guestbook.json。";
  }
}

/* ---------------- 顶部按钮的弹出框逻辑 ---------------- */
function setupPopover(triggerId, popoverId, onOpen) {
  const trigger = document.getElementById(triggerId);
  const popover = document.getElementById(popoverId);
  if (!trigger || !popover) return;

  let hideTimer = null;

  const show = () => {
    if (hideTimer) { clearTimeout(hideTimer); hideTimer = null; }
    popover.classList.add("show");
    if (onOpen) onOpen();
  };

  const hide = () => {
    popover.classList.remove("show");
  };

  const scheduleHide = () => {
    if (hideTimer) clearTimeout(hideTimer);
    hideTimer = setTimeout(hide, 150);
  };

  trigger.addEventListener("mouseenter", show);
  trigger.addEventListener("mouseleave", scheduleHide);

  popover.addEventListener("mouseenter", () => {
    if (hideTimer) { clearTimeout(hideTimer); hideTimer = null; }
  });
  popover.addEventListener("mouseleave", scheduleHide);

  // 点击也可以展开/收起，方便触摸设备使用
  trigger.addEventListener("click", () => {
    if (popover.classList.contains("show")) {
      hide();
    } else {
      show();
    }
  });
}

/* ---------------- 留言弹窗逻辑 ---------------- */
function setupGuestbookModal() {
  const openBtn = document.getElementById("open-guestbook-btn");
  const modalMask = document.getElementById("guestbook-modal");
  const closeBtn = document.getElementById("guestbook-modal-close");
  const cancelBtn = document.getElementById("guestbook-modal-cancel");
  const submitBtn = document.getElementById("guestbook-modal-submit");
  const nickInput = document.getElementById("gb-nickname");
  const contentInput = document.getElementById("gb-content");
  const msgEl = document.getElementById("guestbook-msg");

  if (!openBtn || !modalMask) return;

  const openModal = () => {
    modalMask.classList.add("show");
    if (msgEl) {
      msgEl.textContent = "";
      msgEl.className = "msg";
    }
    nickInput.value = "";
    contentInput.value = "";
    nickInput.focus();
  };

  const closeModal = () => {
    modalMask.classList.remove("show");
  };

  openBtn.addEventListener("click", (e) => {
    e.stopPropagation(); // 防止触发 popover hide
    openModal();
  });

  closeBtn.addEventListener("click", closeModal);
  cancelBtn.addEventListener("click", closeModal);

  modalMask.addEventListener("click", (e) => {
    if (e.target === modalMask) {
      closeModal();
    }
  });

  document.addEventListener("keydown", (e) => {
    if (e.key === "Escape") closeModal();
  });

  submitBtn.addEventListener("click", async () => {
    const nickname = nickInput.value.trim();
    const content = contentInput.value.trim();

    if (!nickname || !content) {
      if (msgEl) {
        msgEl.textContent = "昵称和内容都要填噢～";
        msgEl.className = "msg msg-error";
      }
      return;
    }

    submitBtn.disabled = true;
    submitBtn.textContent = "提交中…";

    try {
      const resp = await fetch("/api/guestbook", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({
          nickname,
          content,
          contact: "" // 后端有这个字段，这里传空即可
        }),
      });
      const data = await resp.json().catch(() => ({}));
      if (!resp.ok || !data.ok) {
        throw new Error(data.error || "提交失败");
      }

      if (msgEl) {
        msgEl.textContent = "留言已提交，感谢！";
        msgEl.className = "msg msg-ok";
      }
      closeModal();
      // 刷新弹出框里的留言列表
      loadGuestbook();
    } catch (e) {
      console.error(e);
      if (msgEl) {
        msgEl.textContent = "提交失败，请稍后再试。";
        msgEl.className = "msg msg-error";
      }
    } finally {
      submitBtn.disabled = false;
      submitBtn.textContent = "提交";
    }
  });
}

/* ---------------- 初始化 ---------------- */
document.addEventListener("DOMContentLoaded", () => {
  // 第一次加载数据
  loadFriends();
  loadGuestbook();

  // 设置悬停/点击显示的弹出框
  setupPopover("nav-friends", "friends-popover", loadFriends);
  setupPopover("nav-guestbook", "guestbook-popover", loadGuestbook);

  // 留言弹窗
  setupGuestbookModal();
});
