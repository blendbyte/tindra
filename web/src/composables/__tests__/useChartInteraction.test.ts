import { describe, it, expect } from 'vitest'
import { useChartInteraction, PAD_LEFT } from '../useChartInteraction'

function fakeEvent(clientX: number, clientY: number, rectLeft: number): MouseEvent {
  return {
    clientX,
    clientY,
    currentTarget: {
      getBoundingClientRect: () => ({
        left: rectLeft,
        top: 0,
        right: rectLeft + 600,
        bottom: 100,
        width: 600,
        height: 100,
        x: rectLeft,
        y: 0,
        toJSON: () => ({}),
      }),
    },
  } as unknown as MouseEvent
}

describe('useChartInteraction', () => {
  describe('handleMouseLeave', () => {
    it('sets hovered to null', () => {
      const { hovered, handleMouseLeave, handleMouseMove } = useChartInteraction()
      handleMouseMove(fakeEvent(200, 50, 100), 10, true, 200)
      expect(hovered.value).not.toBeNull()
      handleMouseLeave()
      expect(hovered.value).toBeNull()
    })
  })

  describe('handleMouseMove', () => {
    it('sets mouseX and mouseY from the event', () => {
      const { mouseX, mouseY, handleMouseMove } = useChartInteraction()
      handleMouseMove(fakeEvent(300, 75, 100), 5, true, 100)
      expect(mouseX.value).toBe(300)
      expect(mouseY.value).toBe(75)
    })

    it('sets hovered to null when n is 0', () => {
      const { hovered, handleMouseMove } = useChartInteraction()
      handleMouseMove(fakeEvent(200, 50, 100), 0, true, 200)
      expect(hovered.value).toBeNull()
    })

    it('calculates bar index: floor(relX * n / cw)', () => {
      const { hovered, handleMouseMove } = useChartInteraction()
      // clientX=200, rect.left=100, PAD_LEFT=44 → relX=56
      // n=10, cw=200, isBar=true → floor(56 * 10 / 200) = floor(2.8) = 2
      handleMouseMove(fakeEvent(200, 50, 100), 10, true, 200)
      expect(hovered.value).toBe(2)
    })

    it('calculates line index: round((relX / cw) * (n - 1))', () => {
      const { hovered, handleMouseMove } = useChartInteraction()
      // relX=56, n=10, cw=200, isBar=false → round((56/200) * 9) = round(2.52) = 3
      handleMouseMove(fakeEvent(200, 50, 100), 10, false, 200)
      expect(hovered.value).toBe(3)
    })

    it('clamps bar index to 0 when relX is negative', () => {
      const { hovered, handleMouseMove } = useChartInteraction()
      // clientX=100, rect.left=100, PAD_LEFT=44 → relX = -44 → idx < 0 → clamp to 0
      handleMouseMove(fakeEvent(100, 50, 100), 5, true, 100)
      expect(hovered.value).toBe(0)
    })

    it('clamps bar index to n-1 when relX exceeds chart width', () => {
      const { hovered, handleMouseMove } = useChartInteraction()
      // clientX=400, rect.left=100, PAD_LEFT=44 → relX=256
      // n=5, cw=100 → idx=floor(256*5/100)=floor(12.8)=12 → clamp to 4
      handleMouseMove(fakeEvent(400, 50, 100), 5, true, 100)
      expect(hovered.value).toBe(4)
    })

    it('clamps line index to n-1 when relX exceeds chart width', () => {
      const { hovered, handleMouseMove } = useChartInteraction()
      // relX=256, n=5, cw=100 → round((256/100)*4)=round(10.24)=10 → clamp to 4
      handleMouseMove(fakeEvent(400, 50, 100), 5, false, 100)
      expect(hovered.value).toBe(4)
    })

    it('handles single-point series (n=1) without dividing by zero', () => {
      const { hovered, handleMouseMove } = useChartInteraction()
      handleMouseMove(fakeEvent(200, 50, 100), 1, false, 200)
      expect(hovered.value).toBe(0)
    })
  })

  describe('PAD_LEFT', () => {
    it('is 44', () => {
      expect(PAD_LEFT).toBe(44)
    })
  })
})
