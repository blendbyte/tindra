import { describe, it, expect, afterEach } from 'vitest'
import { mount } from '@vue/test-utils'
import ChartTooltip from '../ChartTooltip.vue'

const defaultProps = {
  visible: true,
  mouseX: 100,
  mouseY: 200,
  time: '12:30:00',
  lines: [
    { label: 'Requests', value: '42' },
    { label: 'Errors', value: '3' },
  ],
}

function getTip() {
  return document.querySelector('.chart-tip')
}

afterEach(() => {
  document.body.innerHTML = ''
})

describe('ChartTooltip', () => {
  it('renders nothing when visible is false', () => {
    mount(ChartTooltip, {
      props: { ...defaultProps, visible: false },
      attachTo: document.body,
    })
    expect(getTip()).toBeNull()
  })

  it('renders the tooltip when visible is true', () => {
    mount(ChartTooltip, {
      props: defaultProps,
      attachTo: document.body,
    })
    expect(getTip()).not.toBeNull()
  })

  it('displays the time', () => {
    mount(ChartTooltip, {
      props: defaultProps,
      attachTo: document.body,
    })
    const tip = getTip()!
    expect(tip.querySelector('.chart-tip__time')!.textContent).toBe('12:30:00')
  })

  it('renders a row for each line', () => {
    mount(ChartTooltip, {
      props: defaultProps,
      attachTo: document.body,
    })
    const rows = document.querySelectorAll('.chart-tip__row')
    expect(rows).toHaveLength(2)
  })

  it('displays line labels and values', () => {
    mount(ChartTooltip, {
      props: defaultProps,
      attachTo: document.body,
    })
    const rows = document.querySelectorAll('.chart-tip__row')
    expect(rows[0].querySelector('.chart-tip__label')!.textContent).toBe('Requests')
    expect(rows[0].querySelector('.chart-tip__value')!.textContent).toBe('42')
    expect(rows[1].querySelector('.chart-tip__label')!.textContent).toBe('Errors')
    expect(rows[1].querySelector('.chart-tip__value')!.textContent).toBe('3')
  })

  it('positions the tooltip using mouseX and mouseY', () => {
    mount(ChartTooltip, {
      props: { ...defaultProps, mouseX: 150, mouseY: 300 },
      attachTo: document.body,
    })
    const tip = getTip() as HTMLElement
    expect(tip.style.left).toBe('150px')
    expect(tip.style.top).toBe('300px')
  })

  it('renders with an empty lines array', () => {
    mount(ChartTooltip, {
      props: { ...defaultProps, lines: [] },
      attachTo: document.body,
    })
    expect(getTip()).not.toBeNull()
    expect(document.querySelectorAll('.chart-tip__row')).toHaveLength(0)
  })
})
