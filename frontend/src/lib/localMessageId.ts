let nextLocalMessageId = -1

export function createLocalMessageId() {
  const id = nextLocalMessageId
  nextLocalMessageId -= 1
  return id
}
