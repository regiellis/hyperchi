// htmx 4 uses colon-separated event names. Preserve the last useful result on failure.
function requestMessage(event) {
  return event.target.closest('section')?.querySelector('.request-error');
}
document.addEventListener('htmx:before:request', event => {
  const message = requestMessage(event);
  if (message) message.hidden = true;
});
document.addEventListener('htmx:before:response', event => {
  if (event.detail.ctx.response.status < 400) return;
  event.preventDefault();
  const message = requestMessage(event);
  if (message) message.hidden = false;
});
document.addEventListener('htmx:error', event => {
  const message = requestMessage(event);
  if (message) message.hidden = false;
});
