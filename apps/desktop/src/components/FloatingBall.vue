<script lang="ts" setup>
import { ref, onMounted, onBeforeUnmount } from "vue";

const props = defineProps<{
  mode: "SILENT" | "LIGHT" | "ACTIVE";
  loading: boolean;
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

const handleMouseDown = (e: MouseEvent) => {
  if (e.button !== 0) return; // Only left click for drag
  e.preventDefault();
  // TODO: Add edge-snap + optional auto-hide for non-intrusive presence.
  
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
  emit("dblclick");
};

const handleMouseMove = (e: MouseEvent) => {
  if (!dragging.value) return;
  
  const dx = e.screenX - dragStart.value.x;
  const dy = e.screenY - dragStart.value.y;
  
  if (Math.abs(dx) > 3 || Math.abs(dy) > 3) {
    dragMoved.value = true;
    
    // Move window via IPC
    const newX = dragStart.value.winX + dx;
    const newY = dragStart.value.winY + dy;
    
    if ((window as any).luma?.moveWindow) {
      (window as any).luma.moveWindow(newX, newY);
    }
  }
};

const handleMouseUp = () => {
  dragging.value = false;
  window.removeEventListener("mousemove", handleMouseMove);
  window.removeEventListener("mouseup", handleMouseUp);
  
  if (!dragMoved.value) {
    emit("click");
  }
};

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
    }"
    @mousedown="handleMouseDown"
    @dblclick="handleDblClick"
  >
    <div class="orb-inner"></div>
    <div class="orb-ring"></div>
  </div>
</template>

<style scoped>
.orb {
  width: 48px;
  height: 48px;
  border-radius: 50%;
  position: relative;
  cursor: pointer;
  transition: transform 0.2s, filter 0.3s;
  user-select: none;
  -webkit-user-select: none;
}

.orb:active {
  transform: scale(0.95);
}

.orb-inner {
  position: absolute;
  inset: 4px;
  border-radius: 50%;
  background: radial-gradient(circle at 30% 30%, #ffffff, #a0a0a0);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.3);
  z-index: 2;
}

.orb-ring {
  position: absolute;
  inset: 0;
  border-radius: 50%;
  border: 2px solid transparent;
  z-index: 1;
  animation: spin 10s linear infinite;
}

/* Modes */
.orb-silent .orb-inner {
  background: radial-gradient(circle at 30% 30%, #e0e0e0, #9e9e9e);
}

.orb-light .orb-inner {
  background: radial-gradient(circle at 30% 30%, #e0f7fa, #00bcd4);
  box-shadow: 0 0 15px rgba(0, 188, 212, 0.4);
}

.orb-active .orb-inner {
  background: radial-gradient(circle at 30% 30%, #fff3e0, #ff9800);
  box-shadow: 0 0 20px rgba(255, 152, 0, 0.6);
}

/* Loading */
.orb-loading .orb-ring {
  border-top-color: #ffffff;
  border-right-color: rgba(255, 255, 255, 0.5);
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}
</style>
