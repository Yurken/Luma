<script lang="ts" setup>
import { computed, onBeforeUnmount, ref, watch } from "vue";

type Action = {
  action_type: string;
  message: string;
  confidence: number;
  cost: number;
  risk_level: string;
  reason?: string;
  state?: string;
};

const props = defineProps<{
  action: Action | null;
  visible: boolean;
}>();

const emit = defineEmits<{
  (e: "close"): void;
  (e: "feedback", type: "LIKE" | "DISLIKE"): void;
  (e: "sendMessage", text: string): void;
  (e: "implicitFeedback", type: "IGNORED" | "CLOSED"): void;
}>();

const showTextInput = ref(false);
const feedbackText = ref("");
const ignoreDelayMs = 20000;
let ignoreTimer: number | undefined;

const clearIgnoreTimer = () => {
  if (ignoreTimer) {
    clearTimeout(ignoreTimer);
    ignoreTimer = undefined;
  }
};

const scheduleIgnoreTimer = () => {
  if (!props.visible || !props.action) {
    return;
  }
  clearIgnoreTimer();
  ignoreTimer = window.setTimeout(() => {
    emit("implicitFeedback", "IGNORED");
  }, ignoreDelayMs);
};

// Watch for new action (reply) and keep text input open
watch(() => props.action, (newAction, oldAction) => {
  if (newAction && oldAction && newAction !== oldAction) {
    // New reply received, clear input but keep it open
    if (showTextInput.value) {
      feedbackText.value = "";
    }
  }
});

watch(
  () => [props.visible, props.action],
  ([visible, action]) => {
    if (visible && action) {
      scheduleIgnoreTimer();
    } else {
      clearIgnoreTimer();
    }
  },
  { immediate: true }
);

onBeforeUnmount(() => {
  clearIgnoreTimer();
});

const handleQuickFeedback = (type: "LIKE" | "DISLIKE") => {
  clearIgnoreTimer();
  emit("feedback", type);
};

const handleSendMessage = () => {
  if (feedbackText.value.trim()) {
    clearIgnoreTimer();
    emit("sendMessage", feedbackText.value.trim());
    feedbackText.value = "";
    // Keep showTextInput open to continue conversation
  }
};

const toggleTextInput = () => {
  clearIgnoreTimer();
  showTextInput.value = !showTextInput.value;
  if (!showTextInput.value) {
    feedbackText.value = "";
  }
};

const handleClose = (event?: MouseEvent) => {
  if (event) {
    event.preventDefault();
    event.stopPropagation();
  }
  clearIgnoreTimer();
  emit("implicitFeedback", "CLOSED");
  emit("close");
};

const actionColor = computed(() => {
  switch (props.action?.action_type) {
    case "REST_REMINDER": return "#4caf50";
    case "ENCOURAGE": return "#2196f3";
    case "TASK_BREAKDOWN": return "#9c27b0";
    case "REFRAME": return "#ff9800";
    default: return "#607d8b";
  }
});

const actionLabel = computed(() => {
  switch (props.action?.action_type) {
    case "REST_REMINDER": return "休息提醒";
    case "ENCOURAGE": return "鼓励";
    case "TASK_BREAKDOWN": return "任务拆解";
    case "REFRAME": return "换个角度";
    case "DO_NOT_DISTURB": return "勿扰";
    default: return "建议";
  }
});

const reasonText = computed(() => {
  const reason = props.action?.reason?.trim() || "";
  if (!reason || reason === "model_no_reason") {
    return "";
  }
  return reason;
});

const getActionIcon = (actionType?: string) => {
  const icons: Record<string, string> = {
    REST_REMINDER: "💤",
    ENCOURAGE: "💪",
    TASK_BREAKDOWN: "📋",
    REFRAME: "🔄",
    DO_NOT_DISTURB: "🔕",
  };
  return icons[actionType || ""] || "💡";
};
</script>

<template>
  <Transition name="slide-fade">
    <div v-if="visible && action" class="toast-capsule">
      <div class="capsule-content">
        <div class="capsule-icon">{{ getActionIcon(action.action_type) }}</div>
        <div class="capsule-text">
          <p class="capsule-message">{{ action.message }}</p>
          <p v-if="reasonText" class="capsule-reason">{{ reasonText }}</p>
        </div>
        <button class="capsule-close" @click="handleClose" aria-label="关闭">×</button>
      </div>
      <div v-if="showTextInput" class="capsule-input">
        <textarea 
          v-model="feedbackText" 
          placeholder="说说你的想法..."
          rows="2"
          @keydown.enter.ctrl="handleSendMessage"
        ></textarea>
        <button 
          class="send-btn" 
          @click="handleSendMessage"
          :disabled="!feedbackText.trim()"
        >
          发送
        </button>
      </div>
      <div class="capsule-actions">
        <template v-if="!showTextInput">
          <button class="action-btn" @click="handleQuickFeedback('LIKE')" title="有用">👍</button>
          <button class="action-btn" @click="handleQuickFeedback('DISLIKE')" title="没用">👎</button>
        </template>
        <button class="action-btn" @click="toggleTextInput" :title="showTextInput ? '取消输入' : '文字反馈'">
          {{ showTextInput ? '✕' : '💬' }}
        </button>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.toast-capsule {
  position: absolute;
  top: 60px;
  right: 10px;
  min-width: 280px;
  max-width: 360px;
  background: rgba(255, 255, 255, 0.85);
  backdrop-filter: blur(20px) saturate(180%);
  -webkit-backdrop-filter: blur(20px) saturate(180%);
  border-radius: 25px;
  box-shadow: 
    0 8px 32px rgba(0, 0, 0, 0.12),
    inset 0 0 0 0.5px rgba(255, 255, 255, 0.5);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  z-index: 100;
  font-family: -apple-system, BlinkMacSystemFont, "SF Pro Text", "Helvetica Neue", sans-serif;
}

.capsule-content {
  padding: 12px 16px;
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.capsule-icon {
  font-size: 20px;
  line-height: 1;
  flex-shrink: 0;
}

.capsule-text {
  flex: 1;
  min-width: 0;
}

.capsule-message {
  margin: 0;
  font-size: 14px;
  font-weight: 500;
  line-height: 1.4;
  color: rgba(0, 0, 0, 0.85);
}

.capsule-reason {
  margin: 4px 0 0;
  font-size: 12px;
  color: rgba(0, 0, 0, 0.5);
  line-height: 1.3;
}

.capsule-close {
  background: none;
  border: none;
  color: rgba(0, 0, 0, 0.5);
  font-size: 20px;
  line-height: 1;
  cursor: pointer;
  padding: 0;
  width: 20px;
  height: 20px;
  display: flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  transition: all 0.2s ease;
  flex-shrink: 0;
  z-index: 10;
  position: relative;
  pointer-events: auto;
  -webkit-user-select: none;
  user-select: none;
}

.capsule-close:hover {
  background: rgba(0, 0, 0, 0.05);
  color: rgba(0, 0, 0, 0.8);
}

.capsule-input {
  padding: 0 16px 12px;
  display: flex;
  gap: 8px;
  align-items: flex-end;
}

.capsule-input textarea {
  flex: 1;
  padding: 8px 12px;
  border: 1px solid rgba(0, 0, 0, 0.1);
  border-radius: 12px;
  font-size: 13px;
  font-family: inherit;
  background: rgba(255, 255, 255, 0.6);
  resize: none;
  outline: none;
  transition: all 0.2s ease;
}

.capsule-input textarea:focus {
  border-color: #0A84FF;
  background: rgba(255, 255, 255, 0.9);
  box-shadow: 0 0 0 3px rgba(10, 132, 255, 0.1);
}

.send-btn {
  padding: 8px 16px;
  background: #0A84FF;
  color: white;
  border: none;
  border-radius: 12px;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s ease;
  white-space: nowrap;
}

.send-btn:hover:not(:disabled) {
  background: #0071E3;
  transform: scale(1.02);
}

.send-btn:active:not(:disabled) {
  transform: scale(0.98);
}

.send-btn:disabled {
  background: rgba(0, 0, 0, 0.1);
  color: rgba(0, 0, 0, 0.3);
  cursor: not-allowed;
}

.capsule-actions {
  padding: 8px 16px 12px;
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  border-top: 0.5px solid rgba(0, 0, 0, 0.08);
}

.action-btn {
  background: rgba(0, 0, 0, 0.04);
  border: none;
  cursor: pointer;
  font-size: 14px;
  padding: 6px 10px;
  border-radius: 12px;
  transition: all 0.2s ease;
  line-height: 1;
}

.action-btn:hover {
  background: rgba(0, 0, 0, 0.08);
  transform: scale(1.05);
}

.action-btn:active {
  transform: scale(0.95);
}

/* Transitions */
.slide-fade-enter-active,
.slide-fade-leave-active {
  transition: all 0.4s cubic-bezier(0.175, 0.885, 0.32, 1.275);
}

.slide-fade-enter-from,
.slide-fade-leave-to {
  transform: translateY(-10px) scale(0.95);
  opacity: 0;
}

/* 暗黑模式适配 */
@media (prefers-color-scheme: dark) {
  .toast-capsule {
    background: rgba(0, 0, 0, 0.6);
    box-shadow: 
      0 8px 32px rgba(0, 0, 0, 0.3),
      inset 0 0 0 0.5px rgba(255, 255, 255, 0.1);
  }
  
  .capsule-message {
    color: rgba(255, 255, 255, 0.9);
  }
  
  .capsule-reason {
    color: rgba(255, 255, 255, 0.6);
  }
  
  .capsule-close {
    color: rgba(255, 255, 255, 0.6);
  }
  
  .capsule-close:hover {
    background: rgba(255, 255, 255, 0.1);
    color: rgba(255, 255, 255, 0.9);
  }
  
  .capsule-input textarea {
    background: rgba(255, 255, 255, 0.1);
    border-color: rgba(255, 255, 255, 0.15);
    color: rgba(255, 255, 255, 0.9);
  }
  
  .capsule-input textarea:focus {
    background: rgba(255, 255, 255, 0.15);
    border-color: #0A84FF;
  }
  
  .action-btn {
    background: rgba(255, 255, 255, 0.08);
  }
  
  .action-btn:hover {
    background: rgba(255, 255, 255, 0.15);
  }
}
</style>
