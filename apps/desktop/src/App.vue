<script lang="ts" setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";
import FloatingBall from "./components/FloatingBall.vue";
import SuggestionToast from "./components/SuggestionToast.vue";

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

type FocusCurrent = {
  ts_ms: number;
  app_name: string;
  bundle_id?: string;
  pid?: number;
  focus_minutes: number;
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
const focusMonitorEnabled = ref(false);
const focusCurrent = ref<FocusCurrent | null>(null);
const focusError = ref("");
let focusTimer: number | undefined;
const interventionBudget = ref<"low" | "medium" | "high">("medium");
const quietStart = ref("23:30");
const quietEnd = ref("08:00");

const focusMinutesText = computed(() => {
  if (!focusMonitorEnabled.value) {
    return "—";
  }
  if (!focusCurrent.value) {
    return "0.0 分钟";
  }
  return `${focusCurrent.value.focus_minutes.toFixed(1)} 分钟`;
});

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
      throw new Error("Decision request failed");
    }
    // Type assertion for simplicity
    result.value = data as DecisionResponse;
  } catch (err) {
    error.value = err instanceof Error ? err.message : "Unknown error";
  } finally {
    loading.value = false;
  }
};

const handleFeedback = async (type: "LIKE" | "DISLIKE") => {
  if (!result.value?.request_id) return;
  
  try {
    await fetch(`${apiBase}/v1/feedback`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        request_id: result.value.request_id,
        feedback: type,
      }),
    });
    // Close toast/panel after feedback
    result.value = null; 
  } catch (e) {
    console.error("Feedback failed", e);
  }
};

const loadSettings = async () => {
  settingsError.value = "";
  settingsLoading.value = true;
  try {
    const res = await fetch(`${apiBase}/v1/settings`);
    if (!res.ok) throw new Error("加载设置失败");
    const data = await res.json();
    // ... (Simplified settings loading logic) ...
    if (Array.isArray(data)) {
        const map: Record<string, string> = {};
        data.forEach((item: any) => map[item.key] = item.value);
        if (map.intervention_budget) interventionBudget.value = map.intervention_budget as any;
        if (map.focus_monitor_enabled) focusMonitorEnabled.value = map.focus_monitor_enabled === "true";
        if (map.quiet_hours) {
            const parts = map.quiet_hours.split("-");
            if (parts.length === 2) {
                quietStart.value = parts[0].trim();
                quietEnd.value = parts[1].trim();
            }
        }
    }
    if (!isSettingsWindow.value) await fetchFocusCurrent();
  } catch (err) {
    settingsError.value = err instanceof Error ? err.message : "加载设置失败";
  } finally {
    settingsLoading.value = false;
  }
};

const saveSettings = async () => {
  settingsSaving.value = true;
  try {
    const quietHours = `${quietStart.value}-${quietEnd.value}`;
    await fetch(`${apiBase}/v1/settings`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ key: "intervention_budget", value: interventionBudget.value })
    });
    // ... save others ...
  } finally {
    settingsSaving.value = false;
  }
};

const fetchFocusCurrent = async () => {
  if (!focusMonitorEnabled.value) {
    focusCurrent.value = null;
    return;
  }
  try {
    const res = await fetch(`${apiBase}/v1/focus/current`);
    if (res.ok) {
        const data = await res.json();
        focusCurrent.value = data.app_name ? data : null;
    }
  } catch (e) {}
};

const toggleFocusMonitor = () => {
    focusMonitorEnabled.value = !focusMonitorEnabled.value;
    // In real app, save setting immediately
};

const togglePanel = () => {
  if (isSettingsWindow.value) return;
  panelOpen.value = !panelOpen.value;
};

const setIgnoreMouse = (ignore: boolean) => {
  if (ignoreMouseEvents.value === ignore) return;
  ignoreMouseEvents.value = ignore;
  if ((window as any).luma?.setIgnoreMouseEvents) {
    (window as any).luma.setIgnoreMouseEvents(ignore);
  }
};

const handlePointerMove = (event: PointerEvent) => {
  if (isSettingsWindow.value) return;
  const target = document.elementFromPoint(event.clientX, event.clientY);
  const isInteractive = !!target?.closest(".orb, .panel, .toast-card");
  setIgnoreMouse(!isInteractive);
};

onMounted(() => {
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
    // Initially disable mouse ignore so floating ball is clickable
    setIgnoreMouse(false);
    loadSettings();
    focusTimer = window.setInterval(fetchFocusCurrent, 2000);
  }
});

onBeforeUnmount(() => {
  window.removeEventListener("pointermove", handlePointerMove);
  window.removeEventListener("pointerdown", handlePointerMove);
  if (focusTimer) clearInterval(focusTimer);
});
</script>

<template>
  <div class="app-container">
    <!-- Settings Window Mode -->
    <div v-if="isSettingsWindow" class="settings-page">
      <div class="p-6">
        <h1 class="text-2xl font-bold mb-6">Luma 设置</h1>
        <div class="settings-grid">
            <div class="setting-row">
              <label>介入频率</label>
              <div class="segmented">
                <button :class="{ active: interventionBudget === 'low' }" @click="interventionBudget = 'low'">低</button>
                <button :class="{ active: interventionBudget === 'medium' }" @click="interventionBudget = 'medium'">中</button>
                <button :class="{ active: interventionBudget === 'high' }" @click="interventionBudget = 'high'">高</button>
              </div>
            </div>
        </div>
      </div>
    </div>

    <!-- Widget Mode -->
    <div v-else class="widget-container">
      <FloatingBall 
        :mode="currentMode" 
        :loading="loading" 
        @click="togglePanel" 
      />
      
      <SuggestionToast
        :visible="!!result && !panelOpen"
        :action="result?.action || null"
        @close="result = null"
        @feedback="handleFeedback"
      />

      <Transition name="panel">
        <div v-if="panelOpen" class="panel">
          <div class="header">
            <h1>Luma</h1>
            <div class="mode">
              <button v-for="mode in modes" :key="mode" :class="{ active: mode === currentMode }" @click="currentMode = mode">{{ mode }}</button>
            </div>
          </div>

          <textarea v-model="userText" placeholder="有什么想说的..." />
          
          <div class="actions">
            <button class="primary" :disabled="loading" @click="requestSuggestion">{{ loading ? "..." : "发送" }}</button>
          </div>

          <div v-if="result" class="result-card">
             <p>{{ result.action.message }}</p>
             <div class="feedback-row">
                <button @click="handleFeedback('LIKE')">👍</button>
                <button @click="handleFeedback('DISLIKE')">👎</button>
             </div>
          </div>
          
          <div class="focus-status">
             <small>专注: {{ focusMinutesText }}</small>
          </div>
        </div>
      </Transition>
    </div>
  </div>
</template>

<style>
/* Global Reset */
* { box-sizing: border-box; margin: 0; padding: 0; user-select: none; }
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; background: transparent; overflow: hidden; }

.app-container {
  width: 100vw;
  height: 100vh;
  display: flex;
  justify-content: flex-end;
  align-items: flex-start;
  padding: 10px;
}

.widget-container {
  position: relative;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 10px;
}

.panel {
  width: 300px;
  background: white;
  border-radius: 12px;
  padding: 16px;
  box-shadow: 0 4px 20px rgba(0,0,0,0.15);
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.header { display: flex; justify-content: space-between; align-items: center; }
.header h1 { font-size: 16px; font-weight: 600; }

.mode button {
  font-size: 10px;
  padding: 2px 6px;
  border: 1px solid #eee;
  background: white;
  cursor: pointer;
}
.mode button.active { background: #333; color: white; border-color: #333; }

textarea {
  width: 100%;
  height: 60px;
  border: 1px solid #eee;
  border-radius: 8px;
  padding: 8px;
  font-size: 12px;
  resize: none;
}

.actions button.primary {
  width: 100%;
  background: #333;
  color: white;
  border: none;
  padding: 8px;
  border-radius: 6px;
  cursor: pointer;
}

.result-card {
  background: #f5f5f5;
  padding: 10px;
  border-radius: 8px;
  font-size: 13px;
}

.feedback-row {
  display: flex;
  gap: 10px;
  margin-top: 8px;
}

.settings-page {
  background: #f3f4f6;
  width: 100%;
  height: 100%;
  overflow-y: auto;
}

/* Transitions */
.panel-enter-active, .panel-leave-active { transition: all 0.2s ease; }
.panel-enter-from, .panel-leave-to { opacity: 0; transform: translateY(-10px); }
</style>
