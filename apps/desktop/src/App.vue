<script lang="ts" setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

type Mode = "SILENT" | "LIGHT" | "ACTIVE";

type Action = {
  action_type: string;
  message: string;
  confidence: number;
  cost: number;
  risk_level: string;
};

type DecisionResponse = {
  request_id: string;
  context: {
    user_text: string;
    timestamp: number;
    mode: Mode;
    signals: Record<string, string>;
    history_summary: string;
  };
  action: Action;
  policy_version: string;
  latency_ms: number;
  created_at: string;
};

const modes: Mode[] = ["SILENT", "LIGHT", "ACTIVE"];
const currentMode = ref<Mode>("LIGHT");
const userText = ref("");
const result = ref<DecisionResponse | null>(null);
const loading = ref(false);
const error = ref("");

const formattedMode = computed(() => {
  const mapping: Record<Mode, string> = {
    SILENT: "静默",
    LIGHT: "轻度",
    ACTIVE: "积极",
  };
  return mapping[currentMode.value];
});

const apiBase = "http://127.0.0.1:8081";
const panelOpen = ref(false);
const settingsOpen = ref(false);
const dragging = ref(false);
const dragStart = ref({ x: 0, y: 0, winX: 0, winY: 0 });
const dragMoved = ref(false);
const orbRef = ref<HTMLElement | null>(null);
const panelAlign = ref<"left" | "right">("right");

const requestSuggestion = async () => {
  error.value = "";
  loading.value = true;
  const payload = {
    context: {
      user_text: userText.value,
      timestamp: Date.now(),
      mode: currentMode.value,
      signals: {
        hour_of_day: new Date().getHours().toString(),
        session_minutes: "0",
      },
      history_summary: "",
    },
  };

  try {
    const res = await fetch(`${apiBase}/v1/decision`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    if (!res.ok) {
      throw new Error("Decision request failed");
    }
    result.value = (await res.json()) as DecisionResponse;
  } catch (err) {
    error.value = err instanceof Error ? err.message : "Unknown error";
  } finally {
    loading.value = false;
  }
};

const sendFeedback = async (feedback: "LIKE" | "DISLIKE") => {
  if (!result.value) {
    return;
  }
  await fetch(`${apiBase}/v1/feedback`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      request_id: result.value.request_id,
      feedback,
    }),
  });
};

const togglePanel = () => {
  panelOpen.value = !panelOpen.value;
  if (!panelOpen.value) {
    settingsOpen.value = false;
  }
};

const openSettings = () => {
  panelOpen.value = true;
  settingsOpen.value = true;
  updatePanelAlign();
};

const startDrag = (event: PointerEvent) => {
  if (event.button !== 0) {
    return;
  }
  orbRef.value?.setPointerCapture(event.pointerId);
  dragging.value = true;
  dragMoved.value = false;
  dragStart.value = {
    x: event.screenX,
    y: event.screenY,
    winX: window.screenX,
    winY: window.screenY,
  };
};

const onPointerMove = (event: PointerEvent) => {
  if (!dragging.value) {
    return;
  }
  const dx = event.screenX - dragStart.value.x;
  const dy = event.screenY - dragStart.value.y;
  if (Math.abs(dx) + Math.abs(dy) > 3) {
    dragMoved.value = true;
  }
  // window.moveTo(dragStart.value.winX + dx, dragStart.value.winY + dy);
  if ((window as any).luma?.moveWindow) {
    (window as any).luma.moveWindow(dragStart.value.winX + dx, dragStart.value.winY + dy);
  }
};

const onPointerUp = (event: PointerEvent) => {
  if (!dragging.value) {
    return;
  }
  dragging.value = false;
  orbRef.value?.releasePointerCapture(event.pointerId);
  if (!dragMoved.value) {
    togglePanel();
    updatePanelAlign();
  }
};

const updatePanelAlign = () => {
  const orb = orbRef.value;
  if (!orb) {
    return;
  }
  const rect = orb.getBoundingClientRect();
  const centerX = rect.left + rect.width / 2;
  panelAlign.value = centerX < window.innerWidth / 2 ? "left" : "right";
};

onMounted(() => {
  window.addEventListener("resize", updatePanelAlign);
});

onBeforeUnmount(() => {
  window.removeEventListener("resize", updatePanelAlign);
});

watch(panelOpen, (open) => {
  if (open) {
    updatePanelAlign();
  }
});
</script>

<template>
  <div class="floating-shell">
    <button
      class="orb"
      title="Luma"
      @pointerdown="startDrag"
      @pointermove="onPointerMove"
      @pointerup="onPointerUp"
      @pointercancel="onPointerUp"
      @contextmenu.prevent="openSettings"
      ref="orbRef"
    >
      <img 
        class="orb-avatar" 
        src="/assets/robot.svg" 
        alt="Luma bot" 
        draggable="false" 
        style="user-select: none; -webkit-user-drag: none;"
      />
    </button>

    <div v-if="panelOpen" class="panel" :data-align="panelAlign">
      <div class="header">
        <div>
          <h1>Luma 陪伴助手</h1>
          <p>当前模式：{{ formattedMode }}</p>
        </div>
        <div class="mode">
          <button
            v-for="mode in modes"
            :key="mode"
            :class="{ active: mode === currentMode }"
            @click="currentMode = mode"
          >
            {{ mode }}
          </button>
        </div>
      </div>

      <textarea
        v-model="userText"
        placeholder="描述你当前的状态或任务..."
      />

      <div class="actions">
        <button class="primary" :disabled="loading" @click="requestSuggestion">
          {{ loading ? "请求中..." : "请求建议" }}
        </button>
        <button class="secondary" @click="userText = ''">清空</button>
      </div>

      <div v-if="error" class="result">
        <h3>请求失败</h3>
        <p>{{ error }}</p>
      </div>

      <div v-if="result" class="result">
        <h3>建议卡片</h3>
        <p>{{ result.action.message }}</p>
        <p>
          类型：{{ result.action.action_type }} | 置信度：
          {{ result.action.confidence }} | 风险：{{ result.action.risk_level }}
        </p>
        <div class="feedback">
          <button class="secondary" @click="sendFeedback('LIKE')">赞同</button>
          <button class="secondary" @click="sendFeedback('DISLIKE')">不赞同</button>
        </div>
      </div>

      <div v-if="settingsOpen" class="settings">
        <strong>设置（占位）</strong>
        <p>这里将加入介入频率、敏感度、时间段等设置。</p>
      </div>
    </div>
  </div>
</template>
