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
  reason?: string;
  state?: string;
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

type OllamaModelsResponse = {
  models: string[];
};

const modes: Mode[] = ["SILENT", "LIGHT", "ACTIVE"];
const currentMode = ref<Mode>("LIGHT");
// TODO: Add one-click agent disable toggle and per-mode budgets in settings UI.
const userText = ref("");
const result = ref<DecisionResponse | null>(null);
// TODO: Display gateway_decision and action reasons for transparency.
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

const apiBase = "http://127.0.0.1:52123";
const panelOpen = ref(false);
const settingsOpen = ref(false);
const settingsLoading = ref(false);
const settingsSaving = ref(false);
const settingsError = ref("");
// TODO: Add visible status text derived from rule-based focus state machine.
const isSettingsWindow = ref(false);
const ignoreMouseEvents = ref(true);
const focusMonitorEnabled = ref(false);
const focusCurrent = ref<FocusCurrent | null>(null);
const focusError = ref("");
let focusTimer: number | undefined;
// TODO: Add history view using /v1/logs and /v1/focus/recent endpoints.
const interventionBudget = ref<"low" | "medium" | "high">("medium");
const quietStart = ref("23:30");
const quietEnd = ref("08:00");
const ollamaModel = ref("llama3.1:8b");
const ollamaModels = ref<string[]>([]);
const modelLoadError = ref("");
const showModelDropdown = ref(false);
// TODO: Surface learned preferences and "why fewer/more prompts" explanations.

const defaultModels = ["llama3.1:8b", "qwen3:14b", "qwen3:30b", "gemma3:12b"];
const modelOptions = computed(() => {
  return ollamaModels.value.length ? ollamaModels.value : defaultModels;
});

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
  // TODO: Attach derived focus state + app switch counts for explainable prompts.
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
    console.log("[Luma] Sending request:", payload);
    
    // 添加超时控制（15秒）
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), 15000);
    
    const res = await fetch(`${apiBase}/v1/decision`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
      signal: controller.signal,
    });
    
    clearTimeout(timeoutId);
    
    console.log("[Luma] Response status:", res.status, res.statusText);
    console.log("[Luma] Response headers:", Object.fromEntries(res.headers.entries()));
    
    if (!res.ok) {
      const errorText = await res.text();
      console.error("[Luma] Error response:", errorText);
      throw new Error(`Request failed: ${res.status}`);
    }
    
    const contentType = res.headers.get("content-type");
    if (!contentType || !contentType.includes("application/json")) {
      const text = await res.text();
      console.error("[Luma] Non-JSON response:", text);
      throw new Error("Invalid response format");
    }
    
    const data = await res.json();
    console.log("[Luma] Received data:", data);
    result.value = data as DecisionResponse;
  } catch (err) {
    if (err instanceof Error) {
      if (err.name === "AbortError") {
        error.value = "请求超时（15秒），请稍后再试";
      } else {
        error.value = err.message;
      }
    } else {
      error.value = "未知错误";
    }
    console.error("[Luma] Request error:", err);
  } finally {
    loading.value = false;
  }
};

const handleFeedback = async (type: "LIKE" | "DISLIKE") => {
  if (!result.value?.request_id) return;
  // TODO: Record implicit feedback when toast is closed/ignored without response.
  
  try {
    await fetch(`${apiBase}/v1/feedback`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        request_id: result.value.request_id,
        feedback: type,
      }),
    });
    
    // Close toast after feedback
    result.value = null;
  } catch (e) {
    console.error("Feedback failed", e);
  }
};

const handleSendMessage = async (text: string) => {
  if (!text.trim()) return;
  
  loading.value = true;
  error.value = "";
  
  try {
    const payload = {
      context: {
        user_text: text,
        timestamp: Date.now(),
        mode: currentMode.value,
        signals: {
          hour_of_day: new Date().getHours().toString(),
        },
      },
    };
    
    console.log("[Luma] Sending message:", payload);
    
    // 添加超时控制（15秒）
    const controller = new AbortController();
    const timeoutId = setTimeout(() => controller.abort(), 15000);
    
    const res = await fetch(`${apiBase}/v1/decision`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
      signal: controller.signal,
    });
    
    clearTimeout(timeoutId);
    
    console.log("[Luma] Message response status:", res.status);
    
    if (!res.ok) {
      const errorText = await res.text();
      console.error("[Luma] Message error response:", errorText);
      throw new Error(`Request failed: ${res.status}`);
    }
    
    const data = await res.json();
    console.log("[Luma] Message response data:", data);
    result.value = data as DecisionResponse;
  } catch (err) {
    if (err instanceof Error) {
      if (err.name === "AbortError") {
        error.value = "请求超时";
      } else {
        error.value = err.message;
      }
    } else {
      error.value = "Failed to fetch";
    }
    console.error("[Luma] Send message error:", err);
  } finally {
    loading.value = false;
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
        if (map.ollama_model) ollamaModel.value = map.ollama_model;
        if (map.quiet_hours) {
            const parts = map.quiet_hours.split("-");
            if (parts.length === 2) {
                quietStart.value = parts[0].trim();
                quietEnd.value = parts[1].trim();
            }
        }
    }
    // TODO: Load agent_enabled and per-mode budget settings.
    if (!isSettingsWindow.value) await fetchFocusCurrent();
  } catch (err) {
    settingsError.value = err instanceof Error ? err.message : "加载设置失败";
  } finally {
    settingsLoading.value = false;
  }
};

const loadOllamaModels = async () => {
  modelLoadError.value = "";
  try {
    const res = await fetch(`${apiBase}/v1/ollama/models`);
    if (!res.ok) {
      throw new Error("加载模型列表失败");
    }
    const data = (await res.json()) as OllamaModelsResponse;
    if (Array.isArray(data.models)) {
      ollamaModels.value = data.models;
      console.log('[Luma] 成功加载模型列表:', data.models);
    }
  } catch (err) {
    console.error('[Luma] 加载模型列表失败:', err);
    modelLoadError.value = err instanceof Error ? err.message : "加载模型列表失败";
  }
};

const selectModel = (model: string) => {
  ollamaModel.value = model;
  showModelDropdown.value = false;
};

const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement;
  if (!target.closest('.model-input-wrapper')) {
    showModelDropdown.value = false;
  }
};

const saveSettings = async () => {
  settingsError.value = "";
  settingsSaving.value = true;
  try {
    const quietHours = `${quietStart.value}-${quietEnd.value}`;
    const trimmedModel = ollamaModel.value.trim();
    if (!trimmedModel) {
      settingsError.value = "模型名称不能为空";
      return;
    }
    await fetch(`${apiBase}/v1/settings`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ key: "intervention_budget", value: interventionBudget.value })
    });
    await fetch(`${apiBase}/v1/settings`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ key: "ollama_model", value: trimmedModel })
    });
    // TODO: Save quiet hours, focus monitor toggle, agent_enabled, and per-mode budgets.
    // TODO: Add "reset learning" + "rule-only mode" actions in settings.
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
    // TODO: Persist focus monitor setting to backend.
};

const togglePanel = () => {
  if (isSettingsWindow.value) return;
  // TODO: Add hide/show for floating orb without quitting the app.
  panelOpen.value = !panelOpen.value;
};

const requestAutoSuggestion = async () => {
  if (loading.value) return;
  // TODO: Move auto-suggestion to a low-frequency scheduler with budget/cooldown checks.
  
  // 立即打开panel显示加载状态
  panelOpen.value = true;
  result.value = null;
  error.value = "";
  loading.value = true;
  
  console.log('[Luma] 请求AI建议...');
  
  const payload = {
    context: {
      user_text: "",
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
      const errorText = await res.text().catch(() => "");
      throw new Error(`请求失败 (${res.status}): ${errorText || res.statusText}`);
    }
    
    const data = (await res.json()) as DecisionResponse;
    result.value = data;
    console.log('[Luma] 建议获取成功:', result.value);
  } catch (err) {
    error.value = err instanceof Error ? err.message : "未知错误";
    console.error('[Luma] 请求失败:', err);
  } finally {
    loading.value = false;
  }
};

const setIgnoreMouse = (ignore: boolean) => {
  if (ignoreMouseEvents.value === ignore) return;
  ignoreMouseEvents.value = ignore;
  if ((window as any).luma?.setIgnoreMouseEvents) {
    (window as any).luma.setIgnoreMouseEvents(ignore);
  }
};

const handlePointerMove = (event: MouseEvent) => {
  if (isSettingsWindow.value) return;
  const withinViewport =
    event.clientX >= 0 &&
    event.clientX <= window.innerWidth &&
    event.clientY >= 0 &&
    event.clientY <= window.innerHeight;
  const localX = withinViewport ? event.clientX : event.screenX - window.screenX;
  const localY = withinViewport ? event.clientY : event.screenY - window.screenY;
  const target = document.elementFromPoint(localX, localY);
  const isInteractive = !!target?.closest(".orb, .widget-panel, .toast-card");
  setIgnoreMouse(!isInteractive);
};

onMounted(() => {
  window.addEventListener("mousemove", handlePointerMove);
  window.addEventListener("mousedown", handlePointerMove);
  window.addEventListener("click", handleClickOutside);
  // TODO: Record "panel opened" as an implicit feedback signal.
  const params = new URLSearchParams(window.location.search);
  if (params.get("settings") === "1") {
    isSettingsWindow.value = true;
    panelOpen.value = true;
    settingsOpen.value = true;
    document.body.classList.add("settings-window");
    loadSettings();
    loadOllamaModels();
  } else {
    // Initially disable mouse ignore so floating ball is clickable
    setIgnoreMouse(false);
    loadSettings();
    focusTimer = window.setInterval(fetchFocusCurrent, 2000);
  }
});

onBeforeUnmount(() => {
  window.removeEventListener("mousemove", handlePointerMove);
  window.removeEventListener("mousedown", handlePointerMove);
  window.removeEventListener("click", handleClickOutside);
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
            <div class="setting-row">
              <label>Ollama 模型</label>
              <div class="model-input-wrapper">
                <input
                  v-model="ollamaModel"
                  class="settings-input"
                  placeholder="llama3.1:8b"
                  @focus="showModelDropdown = true"
                  @blur="setTimeout(() => showModelDropdown = false, 200)"
                />
                <div v-if="showModelDropdown && modelOptions.length" class="model-dropdown">
                  <div
                    v-for="model in modelOptions"
                    :key="model"
                    class="model-option"
                    @click="selectModel(model)"
                  >
                    {{ model }}
                  </div>
                </div>
              </div>
              <p class="settings-note">模型名称需与 `ollama list` 一致。</p>
              <p v-if="modelLoadError" class="settings-note settings-warning">{{ modelLoadError }}</p>
              <p v-else-if="ollamaModels.length > 0" class="settings-note settings-success">✓ 已加载 {{ ollamaModels.length }} 个模型</p>
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

    <!-- Widget Mode -->
    <div v-else class="widget-container">
      <FloatingBall 
        :mode="currentMode" 
        :loading="loading" 
        @click="requestAutoSuggestion" 
        @dblclick="togglePanel"
      />
      
      <SuggestionToast
        :visible="!!result && !panelOpen"
        :action="result?.action || null"
        @close="result = null"
        @feedback="handleFeedback"
        @sendMessage="handleSendMessage"
      />

      <Transition name="widget-panel">
        <div v-if="panelOpen" class="widget-panel">
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

          <div v-if="loading && !result && !error" class="loading-card">
            <div class="loading-spinner"></div>
            <p>正在思考...</p>
          </div>

          <div v-if="error" class="error-card">
            <p>❌ {{ error }}</p>
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
  position: fixed;
  top: 10px;
  right: 10px;
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  gap: 10px;
  z-index: 1000;
}

.widget-panel {
  position: static;
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

.loading-card {
  background: #f5f9ff;
  padding: 20px;
  border-radius: 8px;
  font-size: 13px;
  text-align: center;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
}

.loading-spinner {
  width: 32px;
  height: 32px;
  border: 3px solid #e0e0e0;
  border-top-color: #2196F3;
  border-radius: 50%;
  animation: spin 0.8s linear infinite;
}

@keyframes spin {
  to { transform: rotate(360deg); }
}

.error-card {
  background: #fee;
  padding: 10px;
  border-radius: 8px;
  font-size: 12px;
  color: #c00;
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
.widget-panel-enter-active, .widget-panel-leave-active { transition: all 0.2s ease; }
.widget-panel-enter-from, .widget-panel-leave-to { opacity: 0; transform: translateY(-10px); }
</style>
