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

type GatewayDecision = {
  decision: string;
  reason: string;
  overridden_action_type?: string;
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
  model_version: string;
  latency_ms: number;
  created_at?: string;
  created_at_ms: number;
  gateway_decision: GatewayDecision;
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
const settingsLoading = ref(false);
const settingsSaving = ref(false);
const settingsError = ref("");
const isSettingsWindow = ref(false);
const ignoreMouseEvents = ref(false);
const interventionBudget = ref<"low" | "medium" | "high">("medium");
const quietStart = ref("23:30");
const quietEnd = ref("08:00");

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
    const data = (await res.json().catch(() => null)) as unknown;
    if (!res.ok) {
      const message =
        isRecord(data) && typeof data.error === "string"
          ? data.error
          : "Decision request failed";
      throw new Error(message);
    }
    if (!isDecisionResponse(data)) {
      const reqId =
        isRecord(data) && typeof data.request_id === "string"
          ? data.request_id
          : "unknown";
      error.value = `响应不合法 (request_id: ${reqId})`;
      result.value = null;
      return;
    }
    result.value = data;
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

const loadSettings = async () => {
  settingsError.value = "";
  settingsLoading.value = true;
  try {
    const res = await fetch(`${apiBase}/v1/settings`);
    if (!res.ok) {
      throw new Error("加载设置失败");
    }
    const data = (await res.json()) as unknown;
    if (!Array.isArray(data)) {
      throw new Error("设置响应不合法");
    }
    const map: Record<string, string> = {};
    data.forEach((item) => {
      if (
        isRecord(item) &&
        typeof item.key === "string" &&
        typeof item.value === "string"
      ) {
        map[item.key] = item.value;
      }
    });
    if (map.intervention_budget === "low" || map.intervention_budget === "medium" || map.intervention_budget === "high") {
      interventionBudget.value = map.intervention_budget;
    }
    if (map.quiet_hours) {
      const parts = map.quiet_hours.split("-");
      if (parts.length === 2) {
        quietStart.value = parts[0].trim();
        quietEnd.value = parts[1].trim();
      }
    }
  } catch (err) {
    settingsError.value = err instanceof Error ? err.message : "加载设置失败";
  } finally {
    settingsLoading.value = false;
  }
};

const upsertSetting = async (key: string, value: string) => {
  const res = await fetch(`${apiBase}/v1/settings`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ key, value }),
  });
  if (!res.ok) {
    throw new Error("保存设置失败");
  }
};

const saveSettings = async () => {
  settingsError.value = "";
  settingsSaving.value = true;
  try {
    const quietHours = `${quietStart.value}-${quietEnd.value}`;
    await Promise.all([
      upsertSetting("intervention_budget", interventionBudget.value),
      upsertSetting("quiet_hours", quietHours),
    ]);
  } catch (err) {
    settingsError.value = err instanceof Error ? err.message : "保存设置失败";
  } finally {
    settingsSaving.value = false;
  }
};

const togglePanel = () => {
  if (isSettingsWindow.value) {
    return;
  }
  panelOpen.value = !panelOpen.value;
  if (!panelOpen.value) {
    settingsOpen.value = false;
  }
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

const setIgnoreMouse = (ignore: boolean) => {
  if (ignoreMouseEvents.value === ignore) {
    return;
  }
  ignoreMouseEvents.value = ignore;
  if ((window as any).luma?.setIgnoreMouseEvents) {
    (window as any).luma.setIgnoreMouseEvents(ignore);
  }
};

const handlePointerMove = (event: PointerEvent) => {
  if (isSettingsWindow.value) {
    return;
  }
  const target = document.elementFromPoint(event.clientX, event.clientY);
  const isInteractive = !!target?.closest(".orb, .panel");
  setIgnoreMouse(!isInteractive);
};

const isRecord = (value: unknown): value is Record<string, unknown> =>
  typeof value === "object" && value !== null;

const isAction = (value: unknown): value is Action => {
  if (!isRecord(value)) {
    return false;
  }
  return (
    typeof value.action_type === "string" &&
    typeof value.message === "string" &&
    typeof value.confidence === "number" &&
    typeof value.cost === "number" &&
    typeof value.risk_level === "string"
  );
};

const isSignals = (value: unknown): value is Record<string, string> => {
  if (!isRecord(value)) {
    return false;
  }
  return Object.values(value).every((entry) => typeof entry === "string");
};

const isContext = (value: unknown): value is DecisionResponse["context"] => {
  if (!isRecord(value)) {
    return false;
  }
  return (
    typeof value.user_text === "string" &&
    typeof value.timestamp === "number" &&
    typeof value.mode === "string" &&
    typeof value.history_summary === "string" &&
    isSignals(value.signals)
  );
};

const isGatewayDecision = (value: unknown): value is GatewayDecision => {
  if (!isRecord(value)) {
    return false;
  }
  if (typeof value.decision !== "string" || typeof value.reason !== "string") {
    return false;
  }
  if (
    value.overridden_action_type !== undefined &&
    typeof value.overridden_action_type !== "string"
  ) {
    return false;
  }
  return true;
};

const isDecisionResponse = (value: unknown): value is DecisionResponse => {
  if (!isRecord(value)) {
    return false;
  }
  return (
    typeof value.request_id === "string" &&
    isContext(value.context) &&
    isAction(value.action) &&
    typeof value.policy_version === "string" &&
    typeof value.model_version === "string" &&
    typeof value.latency_ms === "number" &&
    typeof value.created_at_ms === "number" &&
    isGatewayDecision(value.gateway_decision)
  );
};

onMounted(() => {
  window.addEventListener("resize", updatePanelAlign);
  window.addEventListener("pointermove", handlePointerMove);
  window.addEventListener("pointerdown", handlePointerMove);
  const params = new URLSearchParams(window.location.search);
  if (params.get("settings") === "1") {
    isSettingsWindow.value = true;
    panelOpen.value = true;
    settingsOpen.value = true;
    document.body.classList.add("settings-window");
    loadSettings();
  } else {
    setIgnoreMouse(true);
  }
});

onBeforeUnmount(() => {
  window.removeEventListener("resize", updatePanelAlign);
  window.removeEventListener("pointermove", handlePointerMove);
  window.removeEventListener("pointerdown", handlePointerMove);
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
      v-if="!isSettingsWindow"
      class="orb"
      title="Luma"
      @pointerdown="startDrag"
      @pointermove="onPointerMove"
      @pointerup="onPointerUp"
      @pointercancel="onPointerUp"
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

    <div
      v-if="panelOpen || isSettingsWindow"
      class="panel"
      :data-align="panelAlign"
    >
      <div v-if="!isSettingsWindow">
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
      </div>

      <div v-if="settingsOpen" class="settings">
        <h3>设置</h3>
        <p class="settings-note">右键打开菜单进入设置。</p>

        <div v-if="settingsLoading" class="settings-note">正在加载设置...</div>
        <div v-else class="settings-grid">
          <div class="setting-row">
            <label>介入频率</label>
            <div class="segmented">
              <button
                :class="{ active: interventionBudget === 'low' }"
                @click="interventionBudget = 'low'"
              >
                低
              </button>
              <button
                :class="{ active: interventionBudget === 'medium' }"
                @click="interventionBudget = 'medium'"
              >
                中
              </button>
              <button
                :class="{ active: interventionBudget === 'high' }"
                @click="interventionBudget = 'high'"
              >
                高
              </button>
            </div>
          </div>

          <div class="setting-row">
            <label>安静时段</label>
            <div class="time-range">
              <input type="time" v-model="quietStart" />
              <span>至</span>
              <input type="time" v-model="quietEnd" />
            </div>
          </div>
        </div>

        <div class="settings-actions">
          <button class="primary" :disabled="settingsSaving" @click="saveSettings">
            {{ settingsSaving ? "保存中..." : "保存设置" }}
          </button>
          <span v-if="settingsError" class="settings-error">{{ settingsError }}</span>
        </div>
      </div>
    </div>
  </div>
</template>
