<script setup lang="ts">
import { useUiStore } from '@/stores/ui'
import ModalDialog from './ModalDialog.vue'

const ui = useUiStore()

function close() { ui.shortcutsOpen = false }

interface Shortcut { keys: string[]; label: string }
interface Group { title: string; shortcuts: Shortcut[] }

const groups: Group[] = [
  {
    title: 'Global',
    shortcuts: [
      { keys: ['⌘K'], label: 'Command palette' },
      { keys: ['⌘1'], label: 'Go to Issues' },
      { keys: ['⌘2'], label: 'Go to Performance' },
      { keys: ['⌘3'], label: 'Go to Releases' },
      { keys: ['⌘,'], label: 'Go to Settings' },
      { keys: ['?'], label: 'This help' },
    ],
  },
  {
    title: 'Issue list',
    shortcuts: [
      { keys: ['J', 'K'], label: 'Move down / up' },
      { keys: ['Enter'], label: 'Open issue' },
      { keys: ['/'], label: 'Focus search' },
      { keys: ['X'], label: 'Select / deselect' },
      { keys: ['E'], label: 'Resolve selected' },
      { keys: ['I'], label: 'Ignore selected' },
      { keys: ['U'], label: 'Unignore selected' },
    ],
  },
  {
    title: 'Issue detail',
    shortcuts: [
      { keys: ['[', ']'], label: 'Previous / next issue' },
      { keys: ['←', '→'], label: 'Previous / next event' },
      { keys: ['E'], label: 'Resolve' },
      { keys: ['I'], label: 'Ignore' },
      { keys: ['A'], label: 'Assign' },
      { keys: ['Esc'], label: 'Back to issues' },
    ],
  },
  {
    title: 'Transaction detail',
    shortcuts: [
      { keys: ['J', 'K'], label: 'Navigate spans' },
      { keys: ['Enter'], label: 'Expand span' },
      { keys: ['/'], label: 'Search spans' },
      { keys: ['Esc'], label: 'Clear selection' },
    ],
  },
  {
    title: 'Transaction samples',
    shortcuts: [
      { keys: ['J', 'K'], label: 'Navigate samples' },
      { keys: ['Enter'], label: 'Open transaction' },
    ],
  },
]
</script>

<template>
  <Teleport to="body">
    <ModalDialog v-if="ui.shortcutsOpen" aria-label="Keyboard shortcuts" @close="close">
    <div class="shortcuts-overlay" @mousedown.self.prevent="close">
      <div class="shortcuts-modal">
        <div class="shortcuts-modal__header">
          <span class="shortcuts-modal__title">Keyboard shortcuts</span>
          <button class="shortcuts-modal__close" autofocus aria-label="Close keyboard shortcuts" @click="close">
            <kbd class="nav__kbd">esc</kbd>
          </button>
        </div>
        <div class="shortcuts-modal__body">
          <div v-for="group in groups" :key="group.title" class="shortcuts-group">
            <div class="shortcuts-group__title">{{ group.title }}</div>
            <div v-for="s in group.shortcuts" :key="s.label" class="shortcuts-row">
              <div class="shortcuts-row__keys">
                <kbd v-for="k in s.keys" :key="k" class="shortcut-kbd">{{ k }}</kbd>
              </div>
              <span class="shortcuts-row__label">{{ s.label }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
    </ModalDialog>
  </Teleport>
</template>
