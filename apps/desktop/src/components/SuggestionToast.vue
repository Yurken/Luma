<script lang="ts" setup>
import { computed, ref, watch } from "vue";

type Action = {
  action_type: string;
  message: string;
  confidence: number;
  cost: number;
  risk_level: string;
};

const props = defineProps<{
  action: Action | null;
  visible: boolean;
}>();

const emit = defineEmits<{
  (e: "close"): void;
  (e: "feedback", type: "LIKE" | "DISLIKE"): void;
  (e: "sendMessage", text: string): void;
}>();

const showTextInput = ref(false);
const feedbackText = ref("");

// Watch for new action (reply) and keep text input open
watch(() => props.action, (newAction, oldAction) => {
  if (newAction && oldAction && newAction !== oldAction) {
    // New reply received, clear input but keep it open
    if (showTextInput.value) {
      feedbackText.value = "";
    }
  }
});

const handleQuickFeedback = (type: "LIKE" | "DISLIKE") => {
  emit("feedback", type);
};

const handleSendMessage = () => {
  if (feedbackText.value.trim()) {
    emit("sendMessage", feedbackText.value.trim());
    feedbackText.value = "";
    // Keep showTextInput open to continue conversation
  }
};

const toggleTextInput = () => {
  showTextInput.value = !showTextInput.value;
  if (!showTextInput.value) {
    feedbackText.value = "";
  }
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
</script>

<template>
  <Transition name="slide-fade">
    <div v-if="visible && action" class="toast-card">
      <div class="toast-header" :style="{ backgroundColor: actionColor }">
        <span class="toast-type">{{ actionLabel }}</span>
        <button class="close-btn" @click="emit('close')">×</button>
      </div>
      <div class="toast-body">
        <p class="message">{{ action.message }}</p>
      </div>
      <div v-if="showTextInput" class="toast-input">
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
      <div class="toast-footer">
        <template v-if="!showTextInput">
          <button class="feedback-btn like" @click="handleQuickFeedback('LIKE')" title="有用">👍</button>
          <button class="feedback-btn dislike" @click="handleQuickFeedback('DISLIKE')" title="没用">👎</button>
        </template>
        <button class="feedback-btn text" @click="toggleTextInput" :title="showTextInput ? '取消输入' : '文字反馈'">
          {{ showTextInput ? '✕' : '💬' }}
        </button>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.toast-card {
  position: absolute;
  top: 60px; /* Below the orb */
  right: 10px; /* Align right */
  width: 280px;
  background: white;
  border-radius: 12px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.15);
  overflow: hidden;
  display: flex;
  flex-direction: column;
  z-index: 100;
}

.toast-header {
  padding: 8px 12px;
  display: flex;
  justify-content: space-between;
  align-items: center;
  color: white;
  font-weight: 600;
  font-size: 12px;
}

.close-btn {
  background: none;
  border: none;
  color: white;
  font-size: 16px;
  cursor: pointer;
  padding: 0 4px;
  opacity: 0.8;
}
.close-btn:hover { opacity: 1; }

.toast-body {
  padding: 12px;
  font-size: 14px;
  color: #333;
  line-height: 1.5;
}

.toast-input {
  padding: 0 12px 8px;
  display: flex;
  gap: 8px;
  align-items: flex-end;
}

.toast-input textarea {
  flex: 1;
  padding: 8px;
  border: 1px solid #ddd;
  border-radius: 6px;
  font-size: 13px;
  font-family: inherit;
  resize: none;
  outline: none;
}

.toast-input textarea:focus {
  border-color: #2196f3;
}

.send-btn {
  padding: 8px 16px;
  background: #2196f3;
  color: white;
  border: none;
  border-radius: 6px;
  font-size: 13px;
  font-weight: 500;
  cursor: pointer;
  transition: background 0.2s;
  white-space: nowrap;
}

.send-btn:hover:not(:disabled) {
  background: #1976d2;
}

.send-btn:disabled {
  background: #ccc;
  cursor: not-allowed;
}

.toast-footer {
  padding: 8px 12px;
  background: #f9fafb;
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  border-top: 1px solid #eee;
}

.feedback-btn {
  background: none;
  border: none;
  cursor: pointer;
  font-size: 14px;
  padding: 4px;
  border-radius: 4px;
  transition: background 0.2s;
}
.feedback-btn:hover { background: #e0e0e0; }
.feedback-btn.text { font-size: 16px; }

/* Transitions */
.slide-fade-enter-active,
.slide-fade-leave-active {
  transition: all 0.3s ease-out;
}

.slide-fade-enter-from,
.slide-fade-leave-to {
  transform: translateY(-10px);
  opacity: 0;
}
</style>
