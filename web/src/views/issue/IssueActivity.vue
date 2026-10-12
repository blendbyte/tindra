<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { useQuery, useMutation, useQueryClient } from '@tanstack/vue-query'
import { apiFetch } from '@/api/client'
import type { Comment, IssueHistoryEntry, User } from '@/api/types'
import { useConfirm } from '@/composables/useConfirm'
import { useFormatters } from '@/composables/useFormatters'
import { useTimezone } from '@/composables/useTimezone'
import Icon from '@/components/Icon.vue'
import IssueSection from './IssueSection.vue'
import { issueSectionQueries } from './sectionQueries'

// The issue's comments and status history as one timeline, with the form to
// add a comment. me decides which comments can be edited or deleted; users
// names the people in history entries.
const props = defineProps<{ issueId: string; me?: User | null; users: User[] }>()

const qc = useQueryClient()
const { confirm } = useConfirm()
const { formatRel } = useFormatters()
const tz = useTimezone()

const commentBody = ref('')

// data is a ref, so a destructuring default never applies; derive the
// fallback instead, or the template reads .find on undefined while loading.
const { data: commentsData } = useQuery({
  queryKey: computed(() => issueSectionQueries.comments(props.issueId).queryKey),
  queryFn: (ctx) => issueSectionQueries.comments(props.issueId).queryFn(ctx),
  enabled: computed(() => !!props.issueId),
})
const comments = computed(() => commentsData.value ?? [])

const { data: historyData } = useQuery({
  queryKey: computed(() => issueSectionQueries.history(props.issueId).queryKey),
  queryFn: (ctx) => issueSectionQueries.history(props.issueId).queryFn(ctx),
  enabled: computed(() => !!props.issueId),
})
const history = computed(() => historyData.value ?? [])

const { mutate: postComment, isPending: postingComment } = useMutation({
  mutationFn: (body: string) =>
    apiFetch(`/api/issues/${props.issueId}/comments`, {
      method: 'POST',
      body: JSON.stringify({ body }),
    }),
  onSuccess: () => {
    commentBody.value = ''
    qc.invalidateQueries({ queryKey: ['issues', props.issueId, 'comments'] })
  },
})

// Comment editing state
const editingCommentId = ref<string | null>(null)
const editingBody = ref('')

function startEdit(comment: Comment) {
  editingCommentId.value = comment.id
  editingBody.value = comment.body
}

function cancelEdit() {
  editingCommentId.value = null
  editingBody.value = ''
}

const { mutate: saveEdit, isPending: savingEdit } = useMutation({
  mutationFn: ({ id, body }: { id: string; body: string }) =>
    apiFetch(`/api/comments/${id}`, { method: 'PUT', body: JSON.stringify({ body }) }),
  onSuccess: () => {
    cancelEdit()
    qc.invalidateQueries({ queryKey: ['issues', props.issueId, 'comments'] })
  },
})

const { mutate: deleteComment } = useMutation({
  mutationFn: (id: string) => apiFetch(`/api/comments/${id}`, { method: 'DELETE' }),
  onSuccess: () => qc.invalidateQueries({ queryKey: ['issues', props.issueId, 'comments'] }),
})

async function confirmDeleteComment(id: string) {
  if (await confirm({ title: 'Delete this comment?', confirmLabel: 'Delete comment', danger: true })) deleteComment(id)
}

function canEditComment(comment: Comment) {
  return props.me?.id === comment.user_id
}

function canDeleteComment(comment: Comment) {
  return props.me?.id === comment.user_id || !!props.me?.permissions.manage_issues
}

// The view is reused when moving to the next or previous issue, so a draft
// or an open edit must not carry over to the new issue.
watch(() => props.issueId, () => {
  commentBody.value = ''
  editingCommentId.value = null
})

// Comments outlive their authors; a deleted user's comments have no name or email.
function commentAuthor(c: Comment): string {
  return c.user_name || c.user_email || 'Deleted user'
}

type TimelineItem =
  | { kind: 'comment'; time: string; data: Comment }
  | { kind: 'history'; time: string; data: IssueHistoryEntry }

const timeline = computed<TimelineItem[]>(() => {
  const items: TimelineItem[] = [
    ...(comments.value ?? []).map((c) => ({ kind: 'comment' as const, time: c.created_at, data: c })),
    ...(history.value ?? []).map((h) => ({ kind: 'history' as const, time: h.created_at, data: h })),
  ]
  return items.sort((a, b) => new Date(a.time).getTime() - new Date(b.time).getTime())
})

function historyLabel(entry: IssueHistoryEntry): string {
  switch (entry.event_type) {
    case 'created': return 'Issue opened'
    case 'regressed': return 'Regressed'
    case 'status_changed': {
      const to = entry.details.to as string | undefined
      if (to === 'resolved') return 'Resolved'
      if (to === 'ignored') {
        const until = entry.details.ignore_until as string | undefined
        const count = entry.details.ignore_count_limit as number | undefined
        if (until) {
          const d = new Date(until)
          return `Ignored until ${d.toLocaleDateString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit', timeZone: tz.value })}`
        }
        if (count != null) return `Ignored for ${count.toLocaleString()} occurrence${count === 1 ? '' : 's'}`
        return 'Ignored forever'
      }
      if (to === 'open') return 'Reopened'
      return `Status changed to ${to ?? 'unknown'}`
    }
    case 'assigned': {
      const toId = entry.details.to_id as string | null | undefined
      if (!toId) return 'Unassigned'
      const u = (props.users ?? []).find((u) => u.id === toId)
      return `Assigned to ${u ? (u.name || u.email) : 'someone'}`
    }
    default: return entry.event_type
  }
}

function historyIcon(eventType: string): string {
  switch (eventType) {
    case 'created': return 'circle-dot'
    case 'regressed': return 'alert-triangle'
    case 'status_changed': return 'git-commit-vertical'
    case 'assigned': return 'user'
    default: return 'git-commit-vertical'
  }
}

function actorName(actorId: string | null): string {
  if (!actorId) return 'System'
  const u = (props.users ?? []).find((u) => u.id === actorId)
  return u ? (u.name || u.email) : 'Unknown'
}

function submitComment(e: Event) {
  e.preventDefault()
  if (!commentBody.value.trim()) return
  postComment(commentBody.value.trim())
}
</script>

<template>
  <IssueSection section-key="activity" title="Activity">
    <template #head>
      <span class="section__count">{{ timeline.length }} events</span>
    </template>

    <div class="activity">
      <template v-for="item in timeline" :key="item.kind === 'comment' ? item.data.id : item.data.id">
        <!-- Comment -->
        <div v-if="item.kind === 'comment'" class="activity-item">
          <div class="activity-avatar">
            {{ commentAuthor(item.data as Comment)[0].toUpperCase() }}
          </div>
          <div class="activity-body">
            <div class="activity-meta">
              <span class="activity-author">{{ commentAuthor(item.data as Comment) }}</span>
              <span class="activity-time mono">{{ formatRel(item.time) }}</span>
              <span v-if="(item.data as Comment).updated_at !== (item.data as Comment).created_at" class="activity-edited">(edited)</span>
              <div class="activity-actions">
                <button v-if="canEditComment(item.data as Comment)" class="activity-action" title="Edit" aria-label="Edit comment" @click="startEdit(item.data as Comment)">
                  <Icon name="pencil" :size="11" />
                </button>
                <button v-if="canDeleteComment(item.data as Comment)" class="activity-action activity-action--danger" title="Delete" aria-label="Delete comment" @click="confirmDeleteComment((item.data as Comment).id)">
                  <Icon name="trash" :size="11" />
                </button>
              </div>
            </div>
            <!-- Edit mode -->
            <div v-if="editingCommentId === (item.data as Comment).id" class="activity-edit">
              <textarea
                v-model="editingBody"
                class="activity-compose__input"
                rows="3"
                @keydown.meta.enter="saveEdit({ id: (item.data as Comment).id, body: editingBody.trim() })"
                @keydown.escape="cancelEdit"
              />
              <div class="activity-edit__footer">
                <button class="btn btn--ghost" @click="cancelEdit">Cancel</button>
                <button
                  class="btn btn--primary"
                  :disabled="!editingBody.trim() || savingEdit"
                  @click="saveEdit({ id: (item.data as Comment).id, body: editingBody.trim() })"
                >Save</button>
              </div>
            </div>
            <div v-else class="activity-text">{{ (item.data as Comment).body }}</div>
          </div>
        </div>

        <!-- History event -->
        <div v-else class="activity-event">
          <div class="activity-event__icon">
            <Icon :name="historyIcon((item.data as IssueHistoryEntry).event_type)" :size="12" />
          </div>
          <span class="activity-event__label">{{ historyLabel(item.data as IssueHistoryEntry) }}</span>
          <span class="activity-event__actor">{{ actorName((item.data as IssueHistoryEntry).actor_id) }}</span>
          <span class="activity-event__time mono">{{ formatRel(item.time) }}</span>
        </div>
      </template>

      <div v-if="timeline.length === 0" class="activity-empty">
        No activity yet.
      </div>

      <form class="activity-compose" @submit="submitComment">
        <textarea
          v-model="commentBody"
          class="activity-compose__input"
          placeholder="Leave a comment..."
          rows="3"
          @keydown.meta.enter="submitComment"
        />
        <div class="activity-compose__footer">
          <span class="mono" style="color: var(--text-3); font-size: var(--text-xs)">⌘↵ to submit</span>
          <button
            type="submit"
            class="btn btn--primary"
            :disabled="!commentBody.trim() || postingComment"
          >
            Comment
          </button>
        </div>
      </form>
    </div>
  </IssueSection>
</template>
