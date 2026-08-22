from pwnlib.context import context


class Buffer(object):
    """
    List of strings with some helper routines.

    Example:

        >>> b = Buffer()
        >>> b.add(b"A" * 10)
        >>> b.add(b"B" * 10)
        >>> len(b)
        20
        >>> b.get(1)
        b'A'
        >>> len(b)
        19
        >>> b.get(9999)
        b'AAAAAAAAABBBBBBBBBB'
        >>> len(b)
        0
        >>> b.get(1)
        b''

    Implementation Details:

        Implemented as a list.  Strings are added onto the end.
        The ``0th`` item in the buffer is the oldest item, and
        will be received first.
    """
    def __init__(self, buffer_fill_size = None):
        self.data = [] # Buffer
        self.size = 0  # Length
        self.buffer_fill_size = buffer_fill_size
        self._high_water = None
        self._low_water = None

    def __len__(self):
        """
        >>> b = Buffer()
        >>> b.add(b'lol')
        >>> len(b) == 3
        True
        >>> b.add(b'foobar')
        >>> len(b) == 9
        True
        """
        return self.size

    def __nonzero__(self):
        return len(self) > 0

    def __contains__(self, x):
        """
        >>> b = Buffer()
        >>> b.add(b'asdf')
        >>> b'x' in b
        False
        >>> b.add(b'x')
        >>> b'x' in b
        True
        """
        for b in self.data:
            if x in b:
                return True
        return False

    def index(self, x):
        """
        >>> b = Buffer()
        >>> b.add(b'asdf')
        >>> b.add(b'qwert')
        >>> b.index(b't') == len(b) - 1
        True
        """
        sofar = 0
        for b in self.data:
            if x in b:
                return sofar + b.index(x)
            sofar += len(b)
        raise IndexError()

    def add(self, data):
        """
        Adds data to the buffer.

        Arguments:
            data(str,Buffer): Data to add
        """
        # Fast path for ''
        if not data: return

        if isinstance(data, Buffer):
            self.size += data.size
            self.data += data.data
        else:
            self.size += len(data)
            self.data.append(data)

    def unget(self, data):
        """
        Places data at the front of the buffer.

        Arguments:
            data(str,Buffer): Data to place at the beginning of the buffer.

        Example:

            >>> b = Buffer()
            >>> b.add(b"hello")
            >>> b.add(b"world")
            >>> b.get(5)
            b'hello'
            >>> b.unget(b"goodbye")
            >>> b.get()
            b'goodbyeworld'
        """
        if isinstance(data, Buffer):
            self.data = data.data + self.data
            self.size += data.size
        else:
            self.data.insert(0, data)
            self.size += len(data)

    def get(self, want=float('inf')):
        """
        Retrieves bytes from the buffer.

        Arguments:
            want(int): Maximum number of bytes to fetch

        Returns:
            Data as string

        Example:

            >>> b = Buffer()
            >>> b.add(b'hello')
            >>> b.add(b'world')
            >>> b.get(1)
            b'h'
            >>> b.get()
            b'elloworld'
        """
        # Fast path, get all of the data
        if want >= self.size:
            data   = b''.join(self.data)
            self.size = 0
            self.data = []
            return data

        # Slow path, find the correct-index chunk
        have = 0
        i    = 0
        while want >= have:
            have += len(self.data[i])
            i    += 1

        # Join the chunks, evict from the buffer
        data   = b''.join(self.data[:i])
        self.data = self.data[i:]

        # If the last chunk puts us over the limit,
        # stick the extra back at the beginning.
        if have > want:
            extra = data[want:]
            data  = data[:want]
            self.data.insert(0, extra)

        # Size update
        self.size -= len(data)

        return data

    def get_fill_size(self, size=None):
        """
        Retrieves the default fill size for this buffer class.

        Arguments:
            size (int): (Optional) If set and not None, returns the size variable back.

        Returns:
            Fill size as integer if size is None, else size.
        """
        if size is None:
            size = self.buffer_fill_size

        with context.local(buffer_size=size):
            return context.buffer_size

    def set_watermarks(self, high=None, low=None):
        """set_watermarks(high=None, low=None)

        Set high and/or low water marks used by :attr:`over_high_water`
        and :attr:`under_low_water`.  ``None`` leaves the current value
        unchanged.

        Raises:
            ValueError: If the resulting low water mark is greater than
                the high water mark.

        Examples:

            >>> b = Buffer()
            >>> b.high_water is None and b.low_water is None
            True
            >>> b.over_high_water, b.under_low_water
            (False, False)
            >>> b.set_watermarks(high=10, low=4)
            >>> b.high_water, b.low_water
            (10, 4)
            >>> b.add(b'A' * 10)
            >>> b.over_high_water
            True
            >>> b.get(6)
            b'AAAAAA'
            >>> len(b), b.under_low_water
            (4, True)
            >>> b.set_watermarks(high=3)
            Traceback (most recent call last):
                ...
            ValueError: low water mark cannot exceed high water mark
        """
        new_high = self._high_water if high is None else high
        new_low = self._low_water if low is None else low
        if new_high is not None and new_low is not None and new_low > new_high:
            raise ValueError('low water mark cannot exceed high water mark')
        if high is not None:
            self._high_water = high
        if low is not None:
            self._low_water = low

    @property
    def high_water(self):
        """High water mark, or :const:`None` if unset."""
        return self._high_water

    @property
    def low_water(self):
        """Low water mark, or :const:`None` if unset."""
        return self._low_water

    @property
    def over_high_water(self):
        """:const:`True` when :attr:`size` is at least :attr:`high_water`.

        :const:`False` if the high water mark is unset.
        """
        if self._high_water is None:
            return False
        return self.size >= self._high_water

    @property
    def under_low_water(self):
        """:const:`True` when :attr:`size` is at most :attr:`low_water`.

        :const:`False` if the low water mark is unset.
        """
        if self._low_water is None:
            return False
        return self.size <= self._low_water
