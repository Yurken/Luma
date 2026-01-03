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
    focus_state?: string;
    switch_count?: number;
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

type FocusStateSnapshot = {
  ts_ms: number;
  focus_state: string;
  switch_count: number;
  no_progress_ms: number;
  focus_minutes: number;
  app_name?: string;
  window_title?: string;
};

type EventLog = {
  request_id: string;
  action?: Action;
  final_action?: Action;
  gateway_decision?: GatewayDecision;
  created_at_ms: number;
  user_feedback?: string;
};

type FocusEvent = {
  id: number;
  ts_ms: number;
  app_name: string;
  bundle_id?: string;
  pid?: number;
  duration_ms: number;
  window_title?: string;
};

type OllamaModelsResponse = {
  models: string[];
};

type LearningExplanationResponse = {
  summary?: string;
  explanations?: string[];
};

type FeedbackType =
  | "LIKE"
  | "DISLIKE"
  | "ADOPTED"
  | "IGNORED"
  | "CLOSED"
  | "OPEN_PANEL";

const modes: Mode[] = ["SILENT", "LIGHT", "ACTIVE"];
const currentMode = ref<Mode>("LIGHT");
const userText = ref("");
const result = ref<DecisionResponse | null>(null);
const loading = ref(false);
const error = ref("");

const modeLabels: Record<Mode, string> = {
  SILENT: "静默",
  LIGHT: "轻度",
  ACTIVE: "积极",
};

const actionTypeLabels: Record<string, string> = {
  REST_REMINDER: "休息提醒",
  ENCOURAGE: "鼓励",
  TASK_BREAKDOWN: "任务拆解",
  REFRAME: "换个角度",
  DO_NOT_DISTURB: "勿扰",
};

const formattedMode = computed(() => modeLabels[currentMode.value]);

const apiBase = "http://127.0.0.1:52123";
const panelOpen = ref(false);
const settingsSaving = ref(false);
const settingsError = ref("");
const isSettingsWindow = ref(false);
const ignoreMouseEvents = ref(true);
const focusMonitorEnabled = ref(false);
const focusCurrent = ref<FocusCurrent | null>(null);
const focusState = ref("");
const focusSwitchCount = ref<number | null>(null);
let focusTimer: number | undefined;
let stateTimer: number | undefined;
let autoSuggestTimer: number | undefined;
const lastAutoSuggestAt = ref(0);

const interventionBudget = ref<"low" | "medium" | "high">("medium");
const agentEnabled = ref(true);
const ruleOnlyMode = ref(false);
const budgetSilent = ref("1");
const budgetLight = ref("2");
const budgetActive = ref("3");
const quietStart = ref("23:30");
const quietEnd = ref("08:00");
const ollamaModel = ref("llama3.1:8b");
const ollamaModels = ref<string[]>([]);
const modelLoadError = ref("");
const showModelDropdown = ref(false);
const orbAutoHide = ref(true);

const learningSummary = ref("");
const learningExplanations = ref<string[]>([]);
const learningLoading = ref(false);
const learningError = ref("");

const historyLogs = ref<EventLog[]>([]);
const focusRecent = ref<FocusEvent[]>([]);
const historyLoading = ref(false);
const historyError = ref("");
const historyUpdatedAt = ref<number | null>(null);

const resettingLearning = ref(false);
const resetLearningMessage = ref("");

const defaultModels = ["llama3.1:8b", "qwen3:14b", "qwen3:30b", "gemma3:12b"];
const autoSuggestIntervalsMs: Record<"low" | "medium" | "high", number> = {
  low: 10 * 60 * 1000,
  medium: 5 * 60 * 1000,
  high: 2 * 60 * 1000,
};
const autoSuggestTickMs = 60 * 1000;
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

const focusStateText = computed(() => {
  if (!focusMonitorEnabled.value) {
    return "未启用";
  }
  if (!focusState.value) {
    return "获取中";
  }
  const mapping: Record<string, string> = {
    NO_PROGRESS: "停滞",
    DISTRACTED: "分心",
    FOCUSED: "专注",
    LIGHT: "轻度",
  };
  return mapping[focusState.value] ?? focusState.value;
});

const promptFrequencyHint = computed(() => {
  const match = learningExplanations.value.find((item) => item.startsWith("提示频率偏好"));
  if (!match) {
    return "";
  }
  const value = match.split(":").slice(1).join(":").trim().toLowerCase();
  switch (value) {
    case "low":
      return "系统倾向减少提示";
    case "medium":
      return "系统保持中等提示频率";
    case "high":
      return "系统倾向增加提示";
    default:
      return "系统根据学习偏好调整提示频率";
  }
});

const actionReasonText = computed(() => {
  const reason = result.value?.action?.reason?.trim() || "";
  if (!reason || reason === "model_no_reason") {
    return "";
  }
  return reason;
});

const gatewayDecisionText = computed(() => {
  const decision = result.value?.gateway_decision?.decision;
  if (!decision) return "";
  const mapping: Record<string, string> = {
    ALLOW: "放行",
    DENY: "拦截",
    OVERRIDE: "改写",
  };
  let text = mapping[decision] ?? decision;
  const overridden = result.value?.gateway_decision?.overridden_action_type;
  if (overridden) {
    text = `${text} -> ${actionTypeLabels[overridden] ?? overridden}`;
  }
  return text;
});

const gatewayDecisionReason = computed(() => {
  const reason = result.value?.gateway_decision?.reason?.trim() || "";
  if (!reason) return "";
  const mapping: Record<string, string> = {
    allow: "通过",
    invalid_action_type: "动作类型无效",
    invalid_risk_level: "风险等级无效",
    invalid_confidence: "置信度过低",
    mode_silent_override: "静默模式拦截",
    low_quality_action: "建议质量不足",
    high_risk_blocked: "高风险拦截",
    budget_exhausted: "预算不足",
    cooldown_active: "冷却中",
  };
  return mapping[reason] ?? reason;
});

const modeLabel = (mode: Mode) => modeLabels[mode] ?? mode;

const actionLabel = (actionType?: string) => {
  if (!actionType) return "建议";
  return actionTypeLabels[actionType] ?? actionType;
};

const formatTime = (tsMs: number) => {
  if (!tsMs) return "--:--";
  return new Date(tsMs).toLocaleTimeString("zh-CN", {
    hour: "2-digit",
    minute: "2-digit",
  });
};

const formatDateTime = (tsMs: number) => {
  if (!tsMs) return "";
  return new Date(tsMs).toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
};

const formatDuration = (ms: number) => {
  if (!ms || ms < 0) return "—";
  const minutes = Math.max(1, Math.round(ms / 60000));
  return `${minutes} 分钟`;
};

const isWithinQuietHours = (start: string, end: string) => {
  const parse = (value: string) => {
    const parts = value.split(":");
    if (parts.length !== 2) return null;
    const hours = Number(parts[0]);
    const minutes = Number(parts[1]);
    if (!Number.isFinite(hours) || !Number.isFinite(minutes)) return null;
    return hours * 60 + minutes;
  };
  const startMinutes = parse(start);
  const endMinutes = parse(end);
  if (startMinutes === null || endMinutes === null) return false;
  if (startMinutes === endMinutes) return false;
  const now = new Date();
  const nowMinutes = now.getHours() * 60 + now.getMinutes();
  if (startMinutes < endMinutes) {
    return nowMinutes >= startMinutes && nowMinutes < endMinutes;
  }
  return nowMinutes >= startMinutes || nowMinutes < endMinutes;
};

const toFriendlyError = (err: unknown, fallback: string) => {
  if (err instanceof Error) {
    if (err.name === "AbortError") {
      return fallback;
    }
    const message = err.message?.trim();
    if (!message || message === "Failed to fetch") {
      return fallback;
    }
    if (!/[\u4e00-\u9fa5]/.test(message)) {
      return fallback;
    }
    return message;
  }
  return fallback;
};

const fetchWithTimeout = async (
  input: RequestInfo | URL,
  init: RequestInit = {},
  timeoutMs = 15000
) => {
  const controller = new AbortController();
  const timeoutId = window.setTimeout(() => controller.abort(), timeoutMs);
  try {
    return await fetch(input, { ...init, signal: controller.signal });
  } finally {
    clearTimeout(timeoutId);
  }
};

const buildSignals = (includeSession: boolean) => {
  const signals: Record<string, string> = {
    hour_of_day: new Date().getHours().toString(),
  };
  if (includeSession) {
    signals.session_minutes = "0";
  }
  if (focusState.value) {
    signals.focus_state = focusState.value;
  }
  if (focusSwitchCount.value !== null) {
    signals.switch_count = focusSwitchCount.value.toString();
  }
  return signals;
};

const buildContext = (text: string, includeSession: boolean) => {
  const context: Record<string, any> = {
    user_text: text,
    timestamp: Date.now(),
    mode: currentMode.value,
    signals: buildSignals(includeSession),
    history_summary: "",
  };
  if (focusState.value) {
    context.focus_state = focusState.value;
  }
  if (focusSwitchCount.value !== null) {
    context.switch_count = focusSwitchCount.value;
  }
  return context;
};

const sendFeedback = async (payload: {
  requestId: string;
  feedback: FeedbackType;
  feedbackText?: string;
  context?: Record<string, any>;
}) => {
  const body: Record<string, any> = {
    request_id: payload.requestId,
    feedback: payload.feedback,
  };
  if (payload.feedbackText) {
    body.feedback_text = payload.feedbackText;
  }
  if (payload.context) {
    body.context = payload.context;
  }

  const res = await fetchWithTimeout(`${apiBase}/v1/feedback`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
  });

  if (!res.ok) {
    throw new Error(`发送反馈失败（${res.status}）`);
  }

  try {
    return await res.json();
  } catch {
    return {};
  }
};

const implicitFeedbackSent = new Set<string>();

const sendImplicitFeedback = async (requestId: string, feedback: FeedbackType) => {
  if (!requestId) {
    return;
  }
  const key = `${requestId}:${feedback}`;
  if (implicitFeedbackSent.has(key)) {
    return;
  }
  implicitFeedbackSent.add(key);
  try {
    await sendFeedback({ requestId, feedback });
  } catch (err) {
    console.error("[Always] 隐式反馈失败:", err);
  }
};

const handleImplicitFeedback = (feedback: FeedbackType) => {
  const requestId = result.value?.request_id;
  if (!requestId) {
    return;
  }
  void sendImplicitFeedback(requestId, feedback);
};

const requestSuggestion = async () => {
  error.value = "";
  loading.value = true;
  const payload = {
    context: buildContext(userText.value, true),
  };

  try {
    console.log("[Always] Sending request:", payload);

    const res = await fetchWithTimeout(`${apiBase}/v1/decision`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });

    console.log("[Always] Response status:", res.status, res.statusText);
    console.log("[Always] Response headers:", Object.fromEntries(res.headers.entries()));

    if (!res.ok) {
      const errorText = await res.text();
      console.error("[Always] Error response:", errorText);
      throw new Error(`请求失败（${res.status}）`);
    }

    const contentType = res.headers.get("content-type");
    if (!contentType || !contentType.includes("application/json")) {
      const text = await res.text();
      console.error("[Always] Non-JSON response:", text);
      throw new Error("响应格式不正确");
    }

    const data = await res.json();
    console.log("[Always] Received data:", data);
    result.value = data as DecisionResponse;
  } catch (err) {
    if (err instanceof Error && err.name === "AbortError") {
      error.value = "请求超时（15秒），请稍后再试";
    } else {
      error.value = toFriendlyError(err, "请求失败，请稍后再试");
    }
    console.error("[Always] Request error:", err);
  } finally {
    loading.value = false;
  }
};

const handleFeedback = async (type: "LIKE" | "DISLIKE") => {
  if (!result.value?.request_id) return;

  try {
    await sendFeedback({ requestId: result.value.request_id, feedback: type });
    result.value = null;
  } catch (e) {
    console.error("[Always] 反馈失败", e);
  }
};

const handleSendMessage = async (text: string) => {
  const trimmed = text.trim();
  if (!trimmed || !result.value?.request_id) return;

  loading.value = true;
  error.value = "";

  try {
    const payloadContext = buildContext(trimmed, true);
    const data = await sendFeedback({
      requestId: result.value.request_id,
      feedback: "ADOPTED",
      feedbackText: trimmed,
      context: payloadContext,
    });
    if (data?.reply) {
      result.value = data.reply as DecisionResponse;
    }
  } catch (err) {
    if (err instanceof Error && err.name === "AbortError") {
      error.value = "发送超时，请稍后再试";
    } else {
      error.value = toFriendlyError(err, "发送失败，请稍后再试");
    }
    console.error("[Always] Send message error:", err);
  } finally {
    loading.value = false;
  }
};

const handleToastClose = () => {
  result.value = null;
};

const loadSettings = async () => {
  settingsError.value = "";
  try {
    const res = await fetch(`${apiBase}/v1/settings`);
    if (!res.ok) throw new Error("加载设置失败");
    const data = await res.json();
    if (Array.isArray(data)) {
      const map: Record<string, string> = {};
      data.forEach((item: { key: string; value: string }) => {
        map[item.key] = item.value;
      });
      if (map.intervention_budget) interventionBudget.value = map.intervention_budget as any;
      if (map.focus_monitor_enabled) focusMonitorEnabled.value = map.focus_monitor_enabled === "true";
      if (map.ollama_model) ollamaModel.value = map.ollama_model;
      if (map.agent_enabled) agentEnabled.value = map.agent_enabled === "true";
      if (map.rule_only_mode) ruleOnlyMode.value = map.rule_only_mode === "true";
      if (map.budget_silent) budgetSilent.value = map.budget_silent;
      if (map.budget_light) budgetLight.value = map.budget_light;
      if (map.budget_active) budgetActive.value = map.budget_active;
      if (map.quiet_hours) {
        const parts = map.quiet_hours.split("-");
        if (parts.length === 2) {
          quietStart.value = parts[0].trim();
          quietEnd.value = parts[1].trim();
        }
      }
    }
    if (!isSettingsWindow.value) {
      await fetchFocusCurrent();
      await fetchFocusStateSnapshot();
    }
  } catch (err) {
    settingsError.value = toFriendlyError(err, "加载设置失败");
  }
};

const loadHistory = async () => {
  historyError.value = "";
  historyLoading.value = true;
  try {
    const [logsRes, focusRes] = await Promise.all([
      fetch(`${apiBase}/v1/logs?limit=10`),
      fetch(`${apiBase}/v1/focus/recent?limit=10`),
    ]);
    if (!logsRes.ok) {
      throw new Error("加载建议记录失败");
    }
    const logsData = await logsRes.json();
    historyLogs.value = Array.isArray(logsData) ? logsData : logsData?.logs || [];
    if (focusRes.ok) {
      const focusData = await focusRes.json();
      focusRecent.value = Array.isArray(focusData) ? focusData : [];
    } else {
      focusRecent.value = [];
    }
    historyUpdatedAt.value = Date.now();
  } catch (err) {
    historyError.value = toFriendlyError(err, "加载历史失败");
  } finally {
    historyLoading.value = false;
  }
};

const formatLearningExplanation = (text: string) => {
  const parts = text.split(":");
  if (parts.length < 2) return text;
  const label = parts[0].trim();
  const value = parts.slice(1).join(":").trim();
  if (!value) return text;
  const normalized = value.toLowerCase();
  const mapping: Record<string, string> = {
    low: "低",
    medium: "中",
    high: "高",
    true: "高",
    false: "低",
  };
  const mapped = mapping[normalized];
  if (!mapped) return text;
  return `${label}: ${mapped}`;
};

const loadLearning = async () => {
  learningError.value = "";
  learningLoading.value = true;
  try {
    const res = await fetch(`${apiBase}/v1/learning/explanations?limit=12`);
    if (!res.ok) {
      throw new Error("加载学习偏好失败");
    }
    const data = (await res.json()) as LearningExplanationResponse;
    const summary = data.summary?.trim() || "";
    learningSummary.value = /[\u4e00-\u9fa5]/.test(summary) ? summary : "";
    learningExplanations.value = Array.isArray(data.explanations) ? data.explanations : [];
  } catch (err) {
    learningError.value = toFriendlyError(err, "加载学习偏好失败");
  } finally {
    learningLoading.value = false;
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
      console.log("[Always] 成功加载模型列表:", data.models);
    }
  } catch (err) {
    console.error("[Always] 加载模型列表失败:", err);
    modelLoadError.value = "加载模型列表失败";
  }
};

const selectModel = (model: string) => {
  ollamaModel.value = model;
  showModelDropdown.value = false;
};

const handleClickOutside = (event: MouseEvent) => {
  const target = event.target as HTMLElement;
  if (!target.closest(".model-input-wrapper")) {
    showModelDropdown.value = false;
  }
};

const postSetting = async (key: string, value: string, label: string) => {
  const res = await fetch(`${apiBase}/v1/settings`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ key, value }),
  });
  if (!res.ok) {
    throw new Error(`${label}保存失败`);
  }
};

const normalizeBudgetValue = (value: string, label: string) => {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed < 0) {
    throw new Error(`${label}需为非负数字`);
  }
  return parsed.toString();
};

const isValidTime = (value: string) => /^\d{2}:\d{2}$/.test(value);

const saveSettings = async () => {
  settingsError.value = "";
  settingsSaving.value = true;
  try {
    const quietHours = `${quietStart.value}-${quietEnd.value}`;
    const trimmedModel = ollamaModel.value.trim();
    if (!trimmedModel) {
      throw new Error("模型名称不能为空");
    }
    if (!isValidTime(quietStart.value) || !isValidTime(quietEnd.value)) {
      throw new Error("安静时段格式不正确");
    }
    const silentValue = normalizeBudgetValue(budgetSilent.value, "静默模式预算");
    const lightValue = normalizeBudgetValue(budgetLight.value, "轻度模式预算");
    const activeValue = normalizeBudgetValue(budgetActive.value, "积极模式预算");

    await Promise.all([
      postSetting("intervention_budget", interventionBudget.value, "介入频率"),
      postSetting("ollama_model", trimmedModel, "模型"),
      postSetting("quiet_hours", quietHours, "安静时段"),
      postSetting(
        "focus_monitor_enabled",
        focusMonitorEnabled.value ? "true" : "false",
        "专注监控"
      ),
      postSetting("agent_enabled", agentEnabled.value ? "true" : "false", "智能代理"),
      postSetting("rule_only_mode", ruleOnlyMode.value ? "true" : "false", "规则模式"),
      postSetting("budget_silent", silentValue, "静默预算"),
      postSetting("budget_light", lightValue, "轻度预算"),
      postSetting("budget_active", activeValue, "积极预算"),
    ]);
  } catch (err) {
    settingsError.value = toFriendlyError(err, "保存设置失败");
  } finally {
    settingsSaving.value = false;
  }
};

const resetLearning = async () => {
  resetLearningMessage.value = "";
  resettingLearning.value = true;
  try {
    const res = await fetch(`${apiBase}/v1/memory/reset`, { method: "POST" });
    if (!res.ok) {
      throw new Error("重置学习失败");
    }
    resetLearningMessage.value = "学习数据已重置";
    loadLearning();
    window.setTimeout(() => {
      resetLearningMessage.value = "";
    }, 3000);
  } catch (err) {
    resetLearningMessage.value = "重置学习失败";
  } finally {
    resettingLearning.value = false;
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

const fetchFocusStateSnapshot = async () => {
  try {
    const res = await fetch(`${apiBase}/v1/state/history?limit=1`);
    if (!res.ok) {
      return;
    }
    const data = await res.json();
    if (Array.isArray(data) && data.length > 0) {
      const snapshot = data[0] as FocusStateSnapshot;
      focusState.value = snapshot.focus_state || "";
      focusSwitchCount.value = Number.isFinite(snapshot.switch_count) ? snapshot.switch_count : null;
    } else {
      focusState.value = "";
      focusSwitchCount.value = null;
    }
  } catch (e) {}
};

const toggleFocusMonitor = async () => {
  const nextValue = !focusMonitorEnabled.value;
  focusMonitorEnabled.value = nextValue;
  try {
    await postSetting("focus_monitor_enabled", nextValue ? "true" : "false", "专注监控");
    if (!nextValue) {
      focusCurrent.value = null;
    }
  } catch (err) {
    focusMonitorEnabled.value = !nextValue;
    settingsError.value = toFriendlyError(err, "专注监控设置失败");
  }
};

const togglePanel = () => {
  if (isSettingsWindow.value) return;
  panelOpen.value = !panelOpen.value;
};

const handleOrbClick = () => {
  if (panelOpen.value) {
    panelOpen.value = false;
    return;
  }
  void requestAutoSuggestion({ openPanel: true, source: "manual" });
};

const hideOrb = () => {
  panelOpen.value = false;
  if ((window as any).always?.hideWindow) {
    (window as any).always.hideWindow();
  }
};

const maybeAutoSuggest = () => {
  if (loading.value || panelOpen.value || result.value) return;
  if (!agentEnabled.value) return;
  if (ruleOnlyMode.value) return;
  if (currentMode.value === "SILENT") return;
  if (userText.value.trim()) return;
  if (isWithinQuietHours(quietStart.value, quietEnd.value)) return;
  const interval = autoSuggestIntervalsMs[interventionBudget.value];
  if (Date.now() - lastAutoSuggestAt.value < interval) return;
  lastAutoSuggestAt.value = Date.now();
  void requestAutoSuggestion({ openPanel: false, source: "auto" });
};

const requestAutoSuggestion = async (options: { openPanel?: boolean; source?: "manual" | "auto" } = {}) => {
  if (loading.value) return;

  const openPanel = options.openPanel ?? true;
  if (openPanel) {
    panelOpen.value = true;
  }
  result.value = null;
  error.value = "";
  loading.value = true;
  lastAutoSuggestAt.value = Date.now();

  console.log("[Always] 请求AI建议...");

  const payload = {
    context: buildContext("", true),
  };

  try {
    const res = await fetchWithTimeout(`${apiBase}/v1/decision`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });

    if (!res.ok) {
      throw new Error(`请求失败（${res.status}）`);
    }

    const data = (await res.json()) as DecisionResponse;
    result.value = data;
    console.log("[Always] 建议获取成功:", result.value);
  } catch (err) {
    error.value = toFriendlyError(err, "请求失败");
    console.error("[Always] 请求失败:", err);
  } finally {
    loading.value = false;
  }
};

const setIgnoreMouse = (ignore: boolean) => {
  if (ignoreMouseEvents.value === ignore) return;
  ignoreMouseEvents.value = ignore;
  if ((window as any).always?.setIgnoreMouseEvents) {
    (window as any).always.setIgnoreMouseEvents(ignore);
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
  // 检查所有可能的交互元素：悬浮球、面板、Toast（包括旧的和新的类名）
  const isInteractive = !!target?.closest(".orb, .widget-panel, .toast-card, .toast-capsule");
  setIgnoreMouse(!isInteractive);
};

const handleStorageEvent = (event: StorageEvent) => {
  if (event.key !== "always.orbAutoHide") return;
  if (event.newValue === null) return;
  orbAutoHide.value = event.newValue === "true";
};

watch(
  () => result.value?.context,
  (ctx) => {
    if (!ctx) return;
    if (ctx.focus_state) {
      focusState.value = ctx.focus_state;
    }
    if (typeof ctx.switch_count === "number") {
      focusSwitchCount.value = ctx.switch_count;
    }
  }
);

watch(
  () => result.value?.request_id,
  (requestId) => {
    if (requestId && panelOpen.value) {
      void sendImplicitFeedback(requestId, "OPEN_PANEL");
    }
  }
);

watch(panelOpen, (open) => {
  if ((window as any).always?.setWindowFocusable) {
    (window as any).always.setWindowFocusable(open);
  }
  if (open && result.value?.request_id) {
    void sendImplicitFeedback(result.value.request_id, "OPEN_PANEL");
  }
});

watch(orbAutoHide, (value) => {
  window.localStorage.setItem("always.orbAutoHide", value ? "true" : "false");
});

onMounted(() => {
  window.addEventListener("mousemove", handlePointerMove);
  window.addEventListener("mousedown", handlePointerMove);
  window.addEventListener("click", handleClickOutside);
  window.addEventListener("storage", handleStorageEvent);
  const storedAutoHide = window.localStorage.getItem("always.orbAutoHide");
  if (storedAutoHide !== null) {
    orbAutoHide.value = storedAutoHide === "true";
  }
  const params = new URLSearchParams(window.location.search);
  if (params.get("settings") === "1") {
    isSettingsWindow.value = true;
    panelOpen.value = true;
    document.body.classList.add("settings-window");
    loadSettings();
    loadOllamaModels();
    loadHistory();
    loadLearning();
  } else {
    setIgnoreMouse(false);
    loadSettings();
    focusTimer = window.setInterval(fetchFocusCurrent, 2000);
    stateTimer = window.setInterval(fetchFocusStateSnapshot, 10000);
    autoSuggestTimer = window.setInterval(maybeAutoSuggest, autoSuggestTickMs);
  }
});

onBeforeUnmount(() => {
  window.removeEventListener("mousemove", handlePointerMove);
  window.removeEventListener("mousedown", handlePointerMove);
  window.removeEventListener("click", handleClickOutside);
  window.removeEventListener("storage", handleStorageEvent);
  if (focusTimer) clearInterval(focusTimer);
  if (stateTimer) clearInterval(stateTimer);
  if (autoSuggestTimer) clearInterval(autoSuggestTimer);
});
</script>

<template>
  <div class="app-container">
    <!-- Settings Window Mode -->
    <div v-if="isSettingsWindow" class="settings-page">
      <div class="p-6">
        <h1 class="text-2xl font-bold mb-6">Always 设置</h1>
        <div class="settings-grid">
          <div class="setting-row">
            <label>智能代理</label>
            <div class="toggle-row">
              <button class="toggle" :class="{ active: agentEnabled }" @click="agentEnabled = !agentEnabled">
                <span></span>
              </button>
              <span class="settings-note">{{ agentEnabled ? "已启用" : "已停用" }}</span>
            </div>
          </div>
          <div class="setting-row">
            <label>规则优先模式</label>
            <div class="toggle-row">
              <button class="toggle" :class="{ active: ruleOnlyMode }" @click="ruleOnlyMode = !ruleOnlyMode">
                <span></span>
              </button>
              <span class="settings-note">仅使用规则，不调用模型</span>
            </div>
          </div>
          <div class="setting-row">
            <label>介入频率</label>
            <div class="segmented">
              <button :class="{ active: interventionBudget === 'low' }" @click="interventionBudget = 'low'">
                低
              </button>
              <button :class="{ active: interventionBudget === 'medium' }" @click="interventionBudget = 'medium'">
                中
              </button>
              <button :class="{ active: interventionBudget === 'high' }" @click="interventionBudget = 'high'">
                高
              </button>
            </div>
          </div>
          <div class="setting-row">
            <label>模式预算</label>
            <div class="budget-grid">
              <div class="budget-item">
                <span>静默</span>
                <input v-model="budgetSilent" class="settings-input" type="number" min="0" step="0.1" />
              </div>
              <div class="budget-item">
                <span>轻度</span>
                <input v-model="budgetLight" class="settings-input" type="number" min="0" step="0.1" />
              </div>
              <div class="budget-item">
                <span>积极</span>
                <input v-model="budgetActive" class="settings-input" type="number" min="0" step="0.1" />
              </div>
            </div>
            <p class="settings-note">用于估算不同模式下的干预成本。</p>
          </div>
          <div class="setting-row">
            <label>专注监控</label>
            <div class="toggle-row">
              <button class="toggle" :class="{ active: focusMonitorEnabled }" @click="toggleFocusMonitor">
                <span></span>
              </button>
              <span class="settings-note">{{ focusMonitorEnabled ? "已启用" : "已关闭" }}</span>
            </div>
          </div>
          <div class="setting-row">
            <label>悬浮球淡出</label>
            <div class="toggle-row">
              <button class="toggle" :class="{ active: orbAutoHide }" @click="orbAutoHide = !orbAutoHide">
                <span></span>
              </button>
              <span class="settings-note">{{ orbAutoHide ? "闲置自动淡出" : "保持常亮" }}</span>
            </div>
          </div>
          <div class="setting-row">
            <label>悬浮球可见性</label>
            <div class="toggle-row">
              <button class="secondary" @click="hideOrb">隐藏悬浮球</button>
              <span class="settings-note">可通过托盘或快捷键恢复</span>
            </div>
          </div>
          <div class="setting-row">
            <label>安静时段</label>
            <div class="time-range">
              <input v-model="quietStart" type="time" />
              <span>至</span>
              <input v-model="quietEnd" type="time" />
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
                <div v-for="model in modelOptions" :key="model" class="model-option" @click="selectModel(model)">
                  {{ model }}
                </div>
              </div>
            </div>
            <p class="settings-note">模型名称需与 `ollama list` 一致。</p>
            <p v-if="modelLoadError" class="settings-note settings-warning">{{ modelLoadError }}</p>
            <p v-else-if="ollamaModels.length > 0" class="settings-note settings-success">
              ✓ 已加载 {{ ollamaModels.length }} 个模型
            </p>
          </div>
        </div>

        <div class="settings">
          <div class="history-header">
            <h3>学习偏好</h3>
            <button class="secondary" :disabled="learningLoading" @click="loadLearning">
              {{ learningLoading ? "刷新中..." : "刷新" }}
            </button>
          </div>
          <p v-if="promptFrequencyHint" class="settings-note">为什么提示更多/更少：{{ promptFrequencyHint }}</p>
          <p v-if="learningSummary" class="settings-note">{{ learningSummary }}</p>
          <div class="learning-list">
            <div v-for="(item, index) in learningExplanations" :key="index" class="learning-item">
              {{ formatLearningExplanation(item) }}
            </div>
          </div>
          <p v-if="!learningLoading && learningExplanations.length === 0" class="settings-note">暂无学习偏好</p>
          <p v-if="learningError" class="settings-error">{{ learningError }}</p>
        </div>

        <div class="settings">
          <h3>学习与维护</h3>
          <div class="setting-row">
            <label>学习数据</label>
            <div class="toggle-row">
              <button class="secondary" :disabled="resettingLearning" @click="resetLearning">
                {{ resettingLearning ? "重置中..." : "重置学习" }}
              </button>
              <span v-if="resetLearningMessage" class="settings-note">{{ resetLearningMessage }}</span>
            </div>
          </div>
        </div>

        <div class="settings history-section">
          <div class="history-header">
            <h3>最近记录</h3>
            <button class="secondary" :disabled="historyLoading" @click="loadHistory">
              {{ historyLoading ? "刷新中..." : "刷新" }}
            </button>
          </div>
          <div class="history-grid">
            <div class="history-block">
              <h4>建议</h4>
              <div class="history-list">
                <div v-for="log in historyLogs" :key="log.request_id" class="history-item">
                  <span class="history-title">{{ actionLabel(log.final_action?.action_type || log.action?.action_type) }}</span>
                  <span class="history-meta">{{ formatTime(log.created_at_ms) }}</span>
                </div>
              </div>
              <p v-if="!historyLoading && historyLogs.length === 0" class="settings-note">暂无记录</p>
            </div>
            <div class="history-block">
              <h4>应用切换</h4>
              <div class="history-list">
                <div v-for="event in focusRecent" :key="event.id" class="history-item">
                  <span class="history-title">{{ event.app_name || "未知应用" }}</span>
                  <span class="history-meta">{{ formatDuration(event.duration_ms) }}</span>
                </div>
              </div>
              <p v-if="!historyLoading && focusRecent.length === 0" class="settings-note">暂无记录</p>
            </div>
          </div>
          <p v-if="historyError" class="settings-error">{{ historyError }}</p>
          <p v-else-if="historyUpdatedAt" class="settings-note">更新于 {{ formatDateTime(historyUpdatedAt) }}</p>
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
        :autoHide="orbAutoHide"
        :autoHideDelay="4000"
        @click="handleOrbClick"
        @dblclick="togglePanel"
      />

      <SuggestionToast
        :visible="!!result && !panelOpen"
        :action="result?.action || null"
        @close="handleToastClose"
        @feedback="handleFeedback"
        @implicit-feedback="handleImplicitFeedback"
        @sendMessage="handleSendMessage"
      />

      <Transition name="widget-panel">
        <div v-if="panelOpen" class="widget-panel">
          <div class="header">
            <div class="header-title">
              <h1>Always</h1>
              <span class="mode-caption">模式：{{ formattedMode }}</span>
            </div>
            <div class="header-actions">
              <button class="ghost" @click="hideOrb">隐藏</button>
              <div class="mode">
                <button
                  v-for="mode in modes"
                  :key="mode"
                  :class="{ active: mode === currentMode }"
                  @click="currentMode = mode"
                >
                  {{ modeLabel(mode) }}
                </button>
              </div>
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
            <p v-if="actionReasonText" class="result-meta">原因：{{ actionReasonText }}</p>
            <p v-if="gatewayDecisionText" class="result-meta">
              网关：{{ gatewayDecisionText }}
              <span v-if="gatewayDecisionReason"> / {{ gatewayDecisionReason }}</span>
            </p>
            <div class="feedback-row">
              <button @click="handleFeedback('LIKE')">👍</button>
              <button @click="handleFeedback('DISLIKE')">👎</button>
            </div>
          </div>

          <div class="focus-status">
            <small>专注时长: {{ focusMinutesText }}</small>
            <small>状态: {{ focusStateText }}</small>
            <small v-if="focusMonitorEnabled && focusSwitchCount !== null">切换: {{ focusSwitchCount }} 次</small>
          </div>
        </div>
      </Transition>
    </div>
  </div>
</template>

<style>
/* Global Reset */
* { 
  box-sizing: border-box; 
  margin: 0; 
  padding: 0; 
  user-select: none;
  -webkit-user-drag: none;
}

body { 
  font-family: -apple-system, BlinkMacSystemFont, "SF Pro Text", "SF Pro Display", "Helvetica Neue", sans-serif;
  background: transparent; 
  overflow: hidden;
  -webkit-font-smoothing: antialiased;
  -moz-osx-font-smoothing: grayscale;
}

/* 隐藏滚动条 */
::-webkit-scrollbar {
  display: none;
  width: 0;
  height: 0;
}

/* 允许输入框和可复制文本选择 */
textarea, input {
  user-select: text;
  -webkit-user-select: text;
}

.app-container {
  width: 100vw;
  height: 100vh;
  display: flex;
  justify-content: flex-end;
  align-items: flex-start;
  padding: 10px;
  background: transparent;
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
  background: transparent;
}

.widget-panel {
  position: static;
  width: 320px;
  background: rgba(255, 255, 255, 0.85);
  backdrop-filter: blur(30px) saturate(180%);
  -webkit-backdrop-filter: blur(30px) saturate(180%);
  border-radius: 16px;
  padding: 20px;
  box-shadow: 
    0 10px 40px rgba(0, 0, 0, 0.15),
    inset 0 0 0 0.5px rgba(255, 255, 255, 0.6);
  display: flex;
  flex-direction: column;
  gap: 16px;
  border: 0.5px solid rgba(0, 0, 0, 0.08);
}

.header { 
  display: flex; 
  justify-content: space-between; 
  align-items: center; 
  margin-bottom: 4px;
}
.header h1 { 
  font-size: 18px; 
  font-weight: 600; 
  color: rgba(0, 0, 0, 0.85);
  letter-spacing: -0.01em;
}
.header-title { 
  display: flex; 
  flex-direction: column; 
  gap: 2px; 
}
.header-actions { 
  display: flex; 
  align-items: center; 
  gap: 8px; 
}
.mode-caption { 
  font-size: 11px; 
  color: rgba(0, 0, 0, 0.5);
  font-weight: 400;
}
.ghost {
  border: none;
  background: transparent;
  font-size: 10px;
  color: #666;
  cursor: pointer;
  padding: 2px 4px;
}
.ghost:hover { color: #333; }

.mode {
  display: flex;
  background: rgba(0, 0, 0, 0.05);
  padding: 3px;
  border-radius: 8px;
  gap: 2px;
}

.mode button {
  font-size: 11px;
  padding: 4px 10px;
  border: none;
  background: transparent;
  color: rgba(0, 0, 0, 0.6);
  cursor: pointer;
  border-radius: 6px;
  font-weight: 500;
  transition: all 0.2s ease;
}
.mode button:hover {
  color: rgba(0, 0, 0, 0.8);
}
.mode button.active { 
  background: rgba(0, 0, 0, 0.85); 
  color: white; 
  box-shadow: 0 1px 2px rgba(0, 0, 0, 0.1);
}

textarea {
  width: 100%;
  min-height: 80px;
  border: 1px solid rgba(0, 0, 0, 0.1);
  border-radius: 10px;
  padding: 12px;
  font-size: 13px;
  font-family: inherit;
  resize: none;
  background: rgba(255, 255, 255, 0.6);
  color: rgba(0, 0, 0, 0.85);
  transition: all 0.2s ease;
  line-height: 1.5;
}

textarea:focus {
  outline: none;
  border-color: #0A84FF;
  background: rgba(255, 255, 255, 0.9);
  box-shadow: 0 0 0 3px rgba(10, 132, 255, 0.1);
}

textarea::placeholder {
  color: rgba(0, 0, 0, 0.4);
}

.actions button.primary {
  width: 100%;
  background: rgba(0, 0, 0, 0.85);
  color: white;
  border: none;
  padding: 10px 16px;
  border-radius: 10px;
  cursor: pointer;
  font-size: 13px;
  font-weight: 500;
  font-family: inherit;
  transition: all 0.2s ease;
}

.actions button.primary:hover:not(:disabled) {
  background: rgba(0, 0, 0, 0.9);
  transform: scale(1.01);
}

.actions button.primary:active:not(:disabled) {
  transform: scale(0.99);
}

.actions button.primary:disabled {
  opacity: 0.5;
  cursor: not-allowed;
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
  background: rgba(0, 0, 0, 0.03);
  padding: 14px;
  border-radius: 10px;
  font-size: 13px;
  border: 0.5px solid rgba(0, 0, 0, 0.06);
  line-height: 1.5;
}

.result-card p {
  margin: 0;
  color: rgba(0, 0, 0, 0.85);
}

.result-meta {
  margin-top: 8px;
  font-size: 12px;
  color: rgba(0, 0, 0, 0.5);
}

.feedback-row {
  display: flex;
  gap: 10px;
  margin-top: 8px;
}

.focus-status {
  display: flex;
  flex-direction: column;
  gap: 4px;
  color: rgba(0, 0, 0, 0.5);
  font-size: 11px;
  padding-top: 12px;
  border-top: 0.5px solid rgba(0, 0, 0, 0.06);
}

.focus-status small {
  font-size: 11px;
  color: rgba(0, 0, 0, 0.5);
}

.settings-page {
  background: #f3f4f6;
  width: 100%;
  height: 100%;
  overflow-y: auto;
}

/* Transitions */
.widget-panel-enter-active, .widget-panel-leave-active { 
  transition: all 0.4s cubic-bezier(0.175, 0.885, 0.32, 1.275); 
}
.widget-panel-enter-from, .widget-panel-leave-to { 
  opacity: 0; 
  transform: translateY(-10px) scale(0.95); 
}

/* 暗黑模式适配 */
@media (prefers-color-scheme: dark) {
  .widget-panel {
    background: rgba(0, 0, 0, 0.6);
    border-color: rgba(255, 255, 255, 0.1);
    box-shadow: 
      0 10px 40px rgba(0, 0, 0, 0.4),
      inset 0 0 0 0.5px rgba(255, 255, 255, 0.1);
  }
  
  .header h1 {
    color: rgba(255, 255, 255, 0.9);
  }
  
  .mode-caption {
    color: rgba(255, 255, 255, 0.6);
  }
  
  .mode {
    background: rgba(255, 255, 255, 0.1);
  }
  
  .mode button {
    color: rgba(255, 255, 255, 0.7);
  }
  
  .mode button:hover {
    color: rgba(255, 255, 255, 0.9);
  }
  
  .mode button.active {
    background: rgba(255, 255, 255, 0.9);
    color: rgba(0, 0, 0, 0.85);
  }
  
  textarea {
    background: rgba(255, 255, 255, 0.1);
    border-color: rgba(255, 255, 255, 0.15);
    color: rgba(255, 255, 255, 0.9);
  }
  
  textarea::placeholder {
    color: rgba(255, 255, 255, 0.4);
  }
  
  .actions button.primary {
    background: rgba(255, 255, 255, 0.9);
    color: rgba(0, 0, 0, 0.85);
  }
  
  .actions button.primary:hover:not(:disabled) {
    background: rgba(255, 255, 255, 1);
  }
  
  .result-card {
    background: rgba(255, 255, 255, 0.08);
    border-color: rgba(255, 255, 255, 0.1);
  }
  
  .result-card p {
    color: rgba(255, 255, 255, 0.9);
  }
  
  .result-meta {
    color: rgba(255, 255, 255, 0.6);
  }
  
  .focus-status {
    border-top-color: rgba(255, 255, 255, 0.1);
  }
  
  .focus-status small {
    color: rgba(255, 255, 255, 0.6);
  }
  
  .ghost {
    color: rgba(255, 255, 255, 0.6);
  }
  
  .ghost:hover {
    color: rgba(255, 255, 255, 0.9);
  }
}
</style>
