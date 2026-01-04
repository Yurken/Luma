<script lang="ts" setup>
import { computed, onBeforeUnmount, onMounted, ref, watch } from "vue";

const props = defineProps<{
  mode: "SILENT" | "LIGHT" | "ACTIVE";
  loading: boolean;
  autoHide?: boolean;
  autoHideDelay?: number;
  orbStyle?: "glass" | "infinity" | "pulse" | "orbit";
}>();

const emit = defineEmits<{
  (e: "click"): void;
  (e: "dblclick"): void;
  (e: "drag-end", x: number, y: number): void;
}>();

const orbRef = ref<HTMLElement | null>(null);
const dragging = ref(false);
const dragMoved = ref(false);
const dragStart = ref({ x: 0, y: 0, winX: 0, winY: 0 });
const autoHidden = ref(false);
let autoHideTimer: number | undefined;
const resolvedOrbStyle = computed(() => props.orbStyle ?? "glass");
const infinityGradientId = `orb-infinity-${Math.random().toString(36).slice(2, 9)}`;

const snapThreshold = 24;
const defaultAutoHideDelay = 4000;

const clearAutoHideTimer = () => {
  if (autoHideTimer) {
    clearTimeout(autoHideTimer);
    autoHideTimer = undefined;
  }
};

const scheduleAutoHide = () => {
  if (!props.autoHide || props.loading) {
    return;
  }
  clearAutoHideTimer();
  autoHideTimer = window.setTimeout(() => {
    autoHidden.value = true;
  }, props.autoHideDelay ?? defaultAutoHideDelay);
};

const wakeOrb = () => {
  autoHidden.value = false;
  clearAutoHideTimer();
};

const clamp = (value: number, min: number, max: number) =>
  Math.min(max, Math.max(min, value));

const getWorkArea = async () => {
  if ((window as any).always?.getDisplayBounds) {
    const bounds = await (window as any).always.getDisplayBounds();
    if (bounds?.workArea) {
      return bounds.workArea as { x: number; y: number; width: number; height: number };
    }
  }
  return {
    x: 0,
    y: 0,
    width: window.screen.availWidth,
    height: window.screen.availHeight,
  };
};

const snapToEdge = async () => {
  const workArea = await getWorkArea();
  const winWidth = window.outerWidth || window.innerWidth;
  const winHeight = window.outerHeight || window.innerHeight;
  const currentX = window.screenX;
  const currentY = window.screenY;

  const distances = [
    { edge: "left", value: Math.abs(currentX - workArea.x) },
    { edge: "right", value: Math.abs(workArea.x + workArea.width - (currentX + winWidth)) },
    { edge: "top", value: Math.abs(currentY - workArea.y) },
    { edge: "bottom", value: Math.abs(workArea.y + workArea.height - (currentY + winHeight)) },
  ];
  distances.sort((a, b) => a.value - b.value);
  const nearest = distances[0];
  if (!nearest || nearest.value > snapThreshold) {
    return;
  }

  let snapX = clamp(currentX, workArea.x, workArea.x + workArea.width - winWidth);
  let snapY = clamp(currentY, workArea.y, workArea.y + workArea.height - winHeight);

  switch (nearest.edge) {
    case "left":
      snapX = workArea.x;
      break;
    case "right":
      snapX = workArea.x + workArea.width - winWidth;
      break;
    case "top":
      snapY = workArea.y;
      break;
    case "bottom":
      snapY = workArea.y + workArea.height - winHeight;
      break;
    default:
      break;
  }

  if ((window as any).always?.moveWindow) {
    (window as any).always.moveWindow(snapX, snapY);
  }
  emit("drag-end", snapX, snapY);
};

const handleMouseDown = (e: MouseEvent) => {
  if (e.button !== 0) return; // Only left click for drag
  e.preventDefault();
  wakeOrb();
  dragging.value = true;
  dragMoved.value = false;
  dragStart.value = { 
    x: e.screenX, 
    y: e.screenY,
    winX: window.screenX,
    winY: window.screenY
  };
  
  window.addEventListener("mousemove", handleMouseMove);
  window.addEventListener("mouseup", handleMouseUp);
};

const handleDblClick = (e: MouseEvent) => {
  e.preventDefault();
  wakeOrb();
  emit("dblclick");
};

const handleMouseMove = (e: MouseEvent) => {
  if (!dragging.value) return;
  wakeOrb();
  
  const dx = e.screenX - dragStart.value.x;
  const dy = e.screenY - dragStart.value.y;
  
  if (Math.abs(dx) > 3 || Math.abs(dy) > 3) {
    dragMoved.value = true;
    
    // Move window via IPC
    const newX = dragStart.value.winX + dx;
    const newY = dragStart.value.winY + dy;
    
    if ((window as any).always?.moveWindow) {
      (window as any).always.moveWindow(newX, newY);
    }
  }
};

const handleMouseUp = () => {
  dragging.value = false;
  window.removeEventListener("mousemove", handleMouseMove);
  window.removeEventListener("mouseup", handleMouseUp);
  
  if (!dragMoved.value) {
    emit("click");
    scheduleAutoHide();
    return;
  }
  void snapToEdge();
  scheduleAutoHide();
};

watch(
  () => props.autoHide,
  (enabled) => {
    if (enabled) {
      scheduleAutoHide();
    } else {
      wakeOrb();
    }
  }
);

watch(
  () => props.loading,
  (loading) => {
    if (loading) {
      wakeOrb();
    } else {
      scheduleAutoHide();
    }
  }
);

onMounted(() => {
  scheduleAutoHide();
});

onBeforeUnmount(() => {
  clearAutoHideTimer();
});
</script>

<template>
  <div
    ref="orbRef"
    class="orb"
    :class="{
      'orb-silent': mode === 'SILENT',
      'orb-light': mode === 'LIGHT',
      'orb-active': mode === 'ACTIVE',
      'orb-loading': loading,
      'orb-hidden': autoHidden,
      'orb-style-glass': resolvedOrbStyle === 'glass',
      'orb-style-infinity': resolvedOrbStyle === 'infinity',
      'orb-style-pulse': resolvedOrbStyle === 'pulse',
      'orb-style-orbit': resolvedOrbStyle === 'orbit',
    }"
    @mousedown="handleMouseDown"
    @dblclick="handleDblClick"
    @mouseenter="wakeOrb"
    @mouseleave="scheduleAutoHide"
  >
    <div class="orb-glass"></div>
    <div v-if="resolvedOrbStyle !== 'glass'" class="orb-visual">
      <svg v-if="resolvedOrbStyle === 'infinity'" class="orb-infinity" viewBox="0 0 100 50" aria-hidden="true">
        <defs>
          <linearGradient :id="infinityGradientId" x1="0%" y1="0%" x2="100%" y2="0%">
            <stop offset="0%" style="stop-color: var(--orb-inf-start);" />
            <stop offset="100%" style="stop-color: var(--orb-inf-end);" />
          </linearGradient>
        </defs>
        <path
          class="orb-infinity-track"
          d="M 50,25 C 38,25 28,12 18,12 C 8,12 8,38 18,38 C 28,38 38,25 50,25 C 62,25 72,38 82,38 C 92,38 92,12 82,12 C 72,12 62,25 50,25 Z"
        />
        <path
          class="orb-infinity-stream"
          :style="{ stroke: `url(#${infinityGradientId})` }"
          d="M 50,25 C 38,25 28,12 18,12 C 8,12 8,38 18,38 C 28,38 38,25 50,25 C 62,25 72,38 82,38 C 92,38 92,12 82,12 C 72,12 62,25 50,25 Z"
        />
      </svg>
      <div v-else-if="resolvedOrbStyle === 'pulse'" class="orb-pulse" aria-hidden="true">
        <span class="orb-pulse-ring"></span>
        <span class="orb-pulse-ring"></span>
        <span class="orb-pulse-core"></span>
      </div>
      <div v-else-if="resolvedOrbStyle === 'orbit'" class="orb-orbit" aria-hidden="true">
        <div class="orb-orbit-sat"></div>
        <div class="orb-orbit-center">A</div>
      </div>
    </div>
    <div v-else class="orb-status-dot" :class="{
      'status-idle': mode === 'SILENT',
      'status-active': mode === 'LIGHT',
      'status-warn': mode === 'ACTIVE',
      'status-loading': loading,
    }"></div>
    <div v-if="loading" class="orb-ring"></div>
  </div>
</template>

<style scoped>
.orb {
  width: 50px;
  height: 50px;
  border-radius: 50%;
  position: relative;
  cursor: pointer;
  transition: transform 0.2s cubic-bezier(0.2, 0.8, 0.2, 1), 
              filter 0.3s ease,
              opacity 0.3s ease;
  user-select: none;
  -webkit-user-select: none;
  overflow: visible;
}

.orb:hover {
  transform: scale(1.05);
}

.orb:active {
  transform: scale(0.95);
}

.orb-hidden {
  opacity: 0.35;
  transform: scale(0.92);
  filter: grayscale(0.2);
}

/* 磨砂玻璃背景 */
.orb-glass {
  position: absolute;
  inset: 0;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.1);
  backdrop-filter: blur(10px);
  -webkit-backdrop-filter: blur(10px);
  box-shadow: 
    inset 0 0 0 1px rgba(255, 255, 255, 0.2),
    0 4px 12px rgba(0, 0, 0, 0.15);
  z-index: 1;
  transition: background 0.2s ease;
}

.orb:hover .orb-glass {
  background: rgba(255, 255, 255, 0.15);
}

/* 状态指示点 */
.orb-status-dot {
  position: absolute;
  top: 50%;
  left: 50%;
  transform: translate(-50%, -50%);
  width: 8px;
  height: 8px;
  border-radius: 50%;
  z-index: 2;
  transition: all 0.3s cubic-bezier(0.4, 0, 0.2, 1);
}

.status-idle {
  background-color: var(--orb-glass-dot, #8E8E93);
  box-shadow: 0 0 4px var(--orb-glass-dot-glow, rgba(142, 142, 147, 0.5));
}

.status-active {
  background-color: var(--orb-glass-dot, #0A84FF);
  box-shadow: 0 0 8px var(--orb-glass-dot-glow, rgba(10, 132, 255, 0.6));
  animation: pulse 2s ease-in-out infinite;
}

.status-warn {
  background-color: var(--orb-glass-dot, #FF9F0A);
  box-shadow: 0 0 8px var(--orb-glass-dot-glow, rgba(255, 159, 10, 0.6));
  animation: pulse 1.5s ease-in-out infinite;
}

.status-loading {
  background-color: #0A84FF;
  box-shadow: 0 0 12px rgba(10, 132, 255, 0.8);
  animation: pulse 1s ease-in-out infinite;
}

@keyframes pulse {
  0%, 100% {
    opacity: 1;
    transform: translate(-50%, -50%) scale(1);
  }
  50% {
    opacity: 0.7;
    transform: translate(-50%, -50%) scale(1.2);
  }
}

/* 加载动画环 */
.orb-ring {
  position: absolute;
  inset: -2px;
  border-radius: 50%;
  border: 2px solid transparent;
  border-top-color: var(--orb-accent-soft, rgba(10, 132, 255, 0.6));
  border-right-color: var(--orb-accent-softer, rgba(10, 132, 255, 0.3));
  z-index: 0;
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

/* 暗黑模式适配 */
@media (prefers-color-scheme: dark) {
  .orb-glass {
    background: rgba(0, 0, 0, 0.2);
    box-shadow: 
      inset 0 0 0 1px rgba(255, 255, 255, 0.1),
      0 4px 12px rgba(0, 0, 0, 0.3);
  }
  
  .orb:hover .orb-glass {
    background: rgba(0, 0, 0, 0.25);
  }
}
</style>
