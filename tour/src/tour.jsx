// Guided tours for Flowa, using React Joyride. The rest of the app is plain JS, so this
// bundle mounts its own small React root and exposes window.FlowaTour.
import { useEffect, useRef, useState } from 'react';
import { createRoot } from 'react-dom/client';
import { Joyride, EVENTS, STATUS } from 'react-joyride';

let show = () => {};

// Whether the person asked their system for less motion, now and while the tour is open.
const calm = matchMedia('(prefers-reduced-motion: reduce)');
function useStill() {
  const [still, setStill] = useState(calm.matches);
  useEffect(() => {
    const on = e => setStill(e.matches);
    // An old browser has the setting but not this way of hearing it change; there the tour keeps what it read at the start.
    calm.addEventListener?.('change', on);
    return () => calm.removeEventListener?.('change', on);
  }, []);
  return still;
}

function Tour() {
  const [tour, setTour] = useState(null);
  const still = useStill();
  const controls = useRef(null);
  const ended = useRef(false);
  show = setTour;

  // Escape ends the tour, like the Skip button.
  useEffect(() => {
    if (!tour) return undefined;
    ended.current = false;
    const onKey = e => { if (e.key === 'Escape') controls.current?.skip(); };
    document.addEventListener('keydown', onKey, true);
    return () => document.removeEventListener('keydown', onKey, true);
  }, [tour]);

  if (!tour) return null;
  const c = tour.colors;
  const quiet = { color: c.muted, fontSize: 13 };
  return (
    <Joyride
      key={tour.id}
      run
      continuous
      steps={tour.steps}
      locale={{ last: 'Done', skip: 'Skip tour' }}
      options={{
        primaryColor: c.accent, backgroundColor: c.card, arrowColor: c.card, textColor: c.text,
        overlayColor: 'rgba(0, 0, 0, 0.6)', zIndex: 200, width: 340, spotlightRadius: 12, spotlightPadding: 6,
        skipBeacon: true, showProgress: true, buttons: ['back', 'skip', 'primary'],
        overlayClickAction: false, dismissKeyAction: false, blockTargetInteraction: true,
        scrollOffset: 90, scrollDuration: still ? 0 : 300,
      }}
      styles={{
        tooltip: { borderRadius: 14, fontSize: 14, lineHeight: 1.5, border: `1px solid ${c.line}` },
        tooltipTitle: { fontSize: 15, fontWeight: 650 },
        tooltipContent: { padding: '8px 0 4px' },
        buttonPrimary: { borderRadius: 9, fontSize: 13, fontWeight: 560, padding: '8px 13px' },
        buttonBack: quiet, buttonSkip: quiet,
        // With less motion the step does not fade in and the page jumps to it. The fade of the dark sheet
        // around the highlight is set inside the library; app.css turns that one off, with every other.
        ...(still ? { floater: { transition: 'none' } } : {}),
      }}
      onEvent={(data, ctl) => {
        controls.current = ctl;
        const over = data.type === EVENTS.TOUR_END || data.status === STATUS.FINISHED || data.status === STATUS.SKIPPED;
        if (!over || ended.current) return;
        ended.current = true;
        setTour(null);
        tour.onEnd(data.status);
      }}
    />
  );
}

const root = document.createElement('div');
root.id = 'tour-root';
document.body.appendChild(root);
createRoot(root).render(<Tour />);

window.FlowaTour = {
  start(steps, colors, onEnd) {
    show({ id: Date.now(), steps, colors, onEnd });
  },
  stop() { show(null); },
};
