/**
 * The meter — the one loader in the product.
 *
 * Three bars stepping like a signal meter on a piece of test equipment. It
 * means one thing only: the agent is working on this, right now. It is not a
 * generic "please wait" — where something is merely loading and no agent is
 * acting, use nothing, or a word. The animation lives in globals.css (`.meter`)
 * so reduced-motion can freeze it in one place.
 */
export default function Meter({ className = "" }: { className?: string }) {
  return (
    <span aria-hidden className={`meter ${className}`.trim()}>
      <i />
      <i />
      <i />
    </span>
  );
}
